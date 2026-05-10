"""
Minimal MCP server for benchmark testing.
Returns structured, queryable data so tasks have real answers to find.
"""
import sys, json

# Inventory dataset: 200 products with name, category, stock, price.
# Tasks can grep for specific products or filter by category/stock.
CATEGORIES = ["electronics", "clothing", "books", "food", "tools"]
PRODUCTS = "\n".join(
    f"product_{i:04d}: category={CATEGORIES[i % 5]}, stock={i * 3 % 97}, price=${(i * 13 % 200) + 1}.{(i * 7 % 100):02d}"
    for i in range(1, 201)
)

for line in sys.stdin:
    line = line.strip()
    if not line:
        continue
    msg = json.loads(line)
    m   = msg.get("method", "")
    id_ = msg.get("id")

    if m == "initialize":
        print(json.dumps({"jsonrpc": "2.0", "id": id_, "result": {
            "protocolVersion": "2024-11-05",
            "capabilities": {},
            "serverInfo": {"name": "mock-inventory", "version": "1"},
        }}), flush=True)

    elif m == "tools/list":
        print(json.dumps({"jsonrpc": "2.0", "id": id_, "result": {"tools": [
            {
                "name": "list_products",
                "description": "Returns the full product inventory (200 items).",
                "inputSchema": {"type": "object", "properties": {}},
            },
        ]}}), flush=True)

    elif m == "tools/call":
        name = msg.get("params", {}).get("name", "")
        if name == "list_products":
            print(json.dumps({"jsonrpc": "2.0", "id": id_, "result": {
                "content": [{"type": "text", "text": PRODUCTS}],
            }}), flush=True)
