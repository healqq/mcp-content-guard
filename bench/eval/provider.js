'use strict';

/**
 * Custom promptfoo provider: runs a full OpenAI agentic loop against an MCP
 * server (direct or via mcp-context-guard proxy) and returns the final answer
 * plus cumulative token counts across all turns.
 *
 * Provider config (set in promptfooconfig.yaml under `config:`):
 *   mcpCmd   string[]  Full command to start the MCP server, e.g.
 *                      ["npx","-y","@modelcontextprotocol/server-fetch"]
 *                      or ["./mcp-context-guard.exe","--threshold=10240","--",
 *                          "npx","-y","@modelcontextprotocol/server-fetch"]
 *   maxTurns number    Max tool-call rounds before giving up (default: 8)
 *   model    string    OpenAI model ID (default: gpt-5.4-mini-2026-03-17)
 */

const path             = require('path');
const { spawn }        = require('child_process');
const readline         = require('readline');
const { OpenAI }       = require('openai');
const { pathToFileURL } = require('url');

const DATA_DIR     = path.resolve(__dirname, '../data');
const CODEBASE_DIR = path.resolve(__dirname, '../data/codebase');
const WEBAPP_URL   = pathToFileURL(path.resolve(__dirname, '../data/webapp/index.html')).href;
const REPO_DIR     = path.resolve(__dirname, '../data/react-repo');

// ── MCP client (stdio JSON-RPC 2.0) ─────────────────────────────────────────

class MCPClient {
  constructor(cmd, args) {
    this._proc = spawn(cmd, args, { stdio: ['pipe', 'pipe', 'inherit'], shell: process.platform === 'win32' });
    this._nextId = 1;
    this._pending = new Map(); // id → { resolve, reject, timer }

    const rl = readline.createInterface({ input: this._proc.stdout, crlfDelay: Infinity });
    rl.on('line', raw => {
      let msg;
      try { msg = JSON.parse(raw); } catch { return; }
      if (msg.id === undefined) return; // skip notifications
      const entry = this._pending.get(msg.id);
      if (!entry) return;
      this._pending.delete(msg.id);
      clearTimeout(entry.timer);
      if (msg.error) entry.reject(new Error(`MCP error: ${JSON.stringify(msg.error)}`));
      else entry.resolve(msg.result);
    });

    this._proc.on('exit', code => {
      for (const [, entry] of this._pending) {
        clearTimeout(entry.timer);
        entry.reject(new Error(`MCP process exited with code ${code}`));
      }
      this._pending.clear();
    });
  }

  _request(method, params, timeoutMs = 30_000) {
    return new Promise((resolve, reject) => {
      const id = this._nextId++;
      const timer = setTimeout(() => {
        this._pending.delete(id);
        reject(new Error(`MCP timeout on ${method} (${timeoutMs}ms)`));
      }, timeoutMs);
      this._pending.set(id, { resolve, reject, timer });
      this._proc.stdin.write(JSON.stringify({ jsonrpc: '2.0', id, method, params }) + '\n');
    });
  }

  async initialize() {
    return this._request('initialize', {
      protocolVersion: '2024-11-05',
      capabilities: {},
      clientInfo: { name: 'promptfoo-bench', version: '1' },
    }, 20_000);
  }

  async listTools() {
    const result = await this._request('tools/list', {}, 15_000);
    return result.tools || [];
  }

  async callTool(name, args, timeoutMs = 60_000) {
    return this._request('tools/call', { name, arguments: args }, timeoutMs);
  }

  close() {
    try { this._proc.stdin.end(); } catch {}
    try { this._proc.kill(); } catch {}
  }
}

// ── Provider class ───────────────────────────────────────────────────────────

class MCPBenchProvider {
  constructor(options = {}) {
    this._config = options.config || {};
  }

  id() { return 'mcp-bench'; }

  async callApi(prompt /*, context, options */) {
    const cfg = this._config;
    const resolvedPrompt = prompt
      .replace(/%%DATA_DIR%%/g, DATA_DIR)
      .replace(/%%CODEBASE_DIR%%/g, CODEBASE_DIR)
      .replace(/%%WEBAPP_URL%%/g, WEBAPP_URL)
      .replace(/%%REPO_DIR%%/g, REPO_DIR);
    const rawCmd   = cfg.mcpCmd || [];
    const maxTurns = cfg.maxTurns || 8;
    const model    = cfg.model || 'gpt-5.4-mini-2026-03-17';

    // expand $ENV_VAR / ${ENV_VAR} in command tokens, then resolve relative
    // paths (./foo or ../foo) against the provider.js file location so they
    // work regardless of the working directory promptfoo uses.
    const mcpCmd = rawCmd.map((s, i) => {
      const expanded = s.replace(/\$\{([^}]+)\}|\$([A-Z_][A-Z0-9_]*)/gi,
        (_, a, b) => process.env[a || b] || '');
      if (/^\.\.?[/\\]/.test(expanded)) {
        return path.resolve(__dirname, expanded);
      }
      return expanded;
    });

    if (mcpCmd.length === 0) return { error: 'provider config missing mcpCmd', output: '' };

    // ── start MCP server ──
    const mcp = new MCPClient(mcpCmd[0], mcpCmd.slice(1));
    try {
      await mcp.initialize();
    } catch (err) {
      mcp.close();
      return { error: `MCP init failed: ${err.message}`, output: '' };
    }

    let mcpTools = [];
    try {
      mcpTools = await mcp.listTools();
    } catch (err) {
      mcp.close();
      return { error: `tools/list failed: ${err.message}`, output: '' };
    }

    // MCP inputSchema → OpenAI function tool format
    const tools = mcpTools.map(t => ({
      type: 'function',
      function: {
        name: t.name,
        description: t.description || t.name,
        parameters: t.inputSchema || { type: 'object', properties: {} },
      },
    }));

    // ── agentic loop ──
    const client   = new OpenAI();
    const messages = [{ role: 'user', content: resolvedPrompt }];
    const usage    = { input: 0, output: 0 };
    let finalText  = '';
    let turns      = 0;

    const toolCallLog = []; // { turn, tool, args }

    try {
      for (let i = 0; i < maxTurns; i++) {
        turns = i + 1;

        const response = await client.chat.completions.create({
          model,
          max_completion_tokens: 1024,
          tools: tools.length > 0 ? tools : undefined,
          messages,
        });

        usage.input  += response.usage.prompt_tokens;
        usage.output += response.usage.completion_tokens;

        const msg = response.choices[0].message;
        messages.push(msg);

        if (response.choices[0].finish_reason === 'stop') {
          finalText = msg.content || '';
          break;
        }

        const toolCalls = msg.tool_calls || [];
        if (toolCalls.length === 0) {
          finalText = msg.content || '';
          break;
        }

        // execute each tool call against the MCP server
        for (const call of toolCalls) {
          let content;
          try {
            const args = JSON.parse(call.function.arguments || '{}');
            toolCallLog.push({ turn: turns, tool: call.function.name, args });
            const mcpResult = await mcp.callTool(call.function.name, args);
            // MCP returns content as an array of blocks; flatten to string for OpenAI
            content = mcpContentToString(mcpResult.content);
            if (mcpResult.isError) content = `Tool error: ${content}`;
          } catch (err) {
            content = `Tool call failed: ${err.message}`;
          }
          messages.push({ role: 'tool', tool_call_id: call.id, content });
        }
      }
    } finally {
      mcp.close();
    }

    return {
      output: finalText || '(no text output)',
      tokenUsage: {
        total:      usage.input + usage.output,
        prompt:     usage.input,
        completion: usage.output,
      },
      metadata: {
        turns,
        inputTokens:  usage.input,
        outputTokens: usage.output,
        toolCalls:    toolCallLog,
      },
    };
  }
}

// MCP content blocks → plain string (OpenAI tool result must be a string)
function mcpContentToString(content) {
  if (!Array.isArray(content)) return String(content ?? '');
  return content
    .map(block => {
      if (block.type === 'text') return block.text;
      if (block.type === 'image') return '[image]';
      return JSON.stringify(block);
    })
    .join('\n');
}

module.exports = MCPBenchProvider;
