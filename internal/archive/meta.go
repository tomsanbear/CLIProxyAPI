package archive

// Meta is the Go form of the record meta JSON (spec section 7.1). Field order
// matches the specification so that marshalled fixtures read in the same order.
type Meta struct {
	V            int          `json:"v"`
	RequestID    string       `json:"request_id"`
	Attempt      int          `json:"attempt"`
	TS           string       `json:"ts"`
	CompletedAt  string       `json:"completed_at"`
	Host         string       `json:"host"`
	ProxyVersion string       `json:"proxy_version"`
	Client       MetaClient   `json:"client"`
	Session      MetaSession  `json:"session"`
	Route        MetaRoute    `json:"route"`
	Outcome      MetaOutcome  `json:"outcome"`
	Usage        MetaUsage    `json:"usage"`
	Request      MetaRequest  `json:"request"`
	Response     MetaResponse `json:"response"`
}

// MetaClient describes the inbound HTTP request, without credentials.
type MetaClient struct {
	Method          string              `json:"method"`
	Path            string              `json:"path"`
	Query           string              `json:"query"`
	RemoteAddr      string              `json:"remote_addr"`
	UserAgent       string              `json:"user_agent"`
	Headers         map[string][]string `json:"headers"`
	CallerKeySHA256 string              `json:"caller_key_sha256"`
}

// MetaSession carries the identities the proxy derives for affinity routing.
type MetaSession struct {
	SessionID       string `json:"session_id"`
	ParentSessionID string `json:"parent_session_id"`
	AgentID         string `json:"agent_id"`
	ClientType      string `json:"client_type"`
	Native          bool   `json:"native"`
}

// MetaRoute records which upstream served the attempt.
type MetaRoute struct {
	Provider       string `json:"provider"`
	RequestedModel string `json:"requested_model"`
	UpstreamModel  string `json:"upstream_model"`
	AccountID      string `json:"account_id"`
	AuthType       string `json:"auth_type"`
	BaseURL        string `json:"base_url"`
}

// MetaOutcome is the result delivered to the client for the attempt.
type MetaOutcome struct {
	Status         int    `json:"status"`
	UpstreamStatus int    `json:"upstream_status"`
	Failed         bool   `json:"failed"`
	ErrorCode      string `json:"error_code"`
	ErrorMessage   string `json:"error_message"`
	StopReason     string `json:"stop_reason"`
	LatencyMS      int64  `json:"latency_ms"`
	TTFTMS         int64  `json:"ttft_ms"`
}

// MetaUsage is the token accounting from the proxy's usage record.
type MetaUsage struct {
	InputTokens           int64 `json:"input_tokens"`
	OutputTokens          int64 `json:"output_tokens"`
	ReasoningTokens       int64 `json:"reasoning_tokens"`
	CacheReadTokens       int64 `json:"cache_read_tokens"`
	CacheCreationTokens   int64 `json:"cache_creation_tokens"`
	CacheCreation5mTokens int64 `json:"cache_creation_5m_tokens"`
	CacheCreation1hTokens int64 `json:"cache_creation_1h_tokens"`
}

// MetaRequest describes the stored request body.
type MetaRequest struct {
	Format        string `json:"format"`
	ContentType   string `json:"content_type"`
	Bytes         int64  `json:"bytes"`
	SHA256        string `json:"sha256"`
	Omitted       bool   `json:"omitted"`
	OmittedReason string `json:"omitted_reason"`
}

// MetaResponse describes the stored response body.
type MetaResponse struct {
	Kind          string `json:"kind"`
	ContentType   string `json:"content_type"`
	Bytes         int64  `json:"bytes"`
	SHA256        string `json:"sha256"`
	Omitted       bool   `json:"omitted"`
	OmittedReason string `json:"omitted_reason"`
}
