package types

type Request struct {
	Method string `json:"method"`
	URL    string `json:"url"`
	Body   string `json:"body"`
}

type Response struct {
	StatusCode int    `json:"status_code"`
	Body       string `json:"body"`
}
