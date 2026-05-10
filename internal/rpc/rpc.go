package rpc

import "encoding/json"

type Message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   json.RawMessage `json:"error,omitempty"`
}

func (m Message) IsRequest() bool {
	return m.Method != "" && m.ID != nil
}

func (m Message) IsNotification() bool {
	return m.Method != "" && m.ID == nil
}

func (m Message) IsResponse() bool {
	return m.Method == "" && m.ID != nil
}

func EncodeTextResult(id any, text string) []byte {
	type block struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	type result struct {
		Content []block `json:"content"`
	}
	type resp struct {
		JSONRPC string `json:"jsonrpc"`
		ID      any    `json:"id"`
		Result  result `json:"result"`
	}
	b, _ := json.Marshal(resp{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result{Content: []block{{Type: "text", Text: text}}},
	})
	return b
}

func EncodeErrorResult(id any, msg string) []byte {
	type block struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	type result struct {
		Content []block `json:"content"`
		IsError bool    `json:"isError"`
	}
	type resp struct {
		JSONRPC string `json:"jsonrpc"`
		ID      any    `json:"id"`
		Result  result `json:"result"`
	}
	b, _ := json.Marshal(resp{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result{Content: []block{{Type: "text", Text: msg}}, IsError: true},
	})
	return b
}
