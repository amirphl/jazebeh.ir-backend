package observability

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/requestid"
)

type SentryConfig struct {
	DSN         string
	Environment string
	Release     string
	ServerName  string
	Timeout     time.Duration
	Capture4xx  bool
	Capture5xx  bool
	// HTTPTransport is injected by the application so Sentry follows the same
	// outbound network policy as every other Go HTTP client.
	HTTPTransport http.RoundTripper
}

type sentryClient struct {
	logger     sentry.Logger
	timeout    time.Duration
	capture4xx bool
	capture5xx bool
}

var (
	clientMu       sync.RWMutex
	activeClient   *sentryClient
	stdLogPrefixRE = regexp.MustCompile(`^\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2}(?:\.\d{6})?(?: [^:]+:\d+:)? `)
)

func InitSentry(cfg SentryConfig) error {
	dsn := strings.TrimSpace(cfg.DSN)
	if dsn == "" {
		clientMu.Lock()
		activeClient = nil
		clientMu.Unlock()
		return nil
	}

	serverName := strings.TrimSpace(cfg.ServerName)
	if serverName == "" {
		serverName, _ = os.Hostname()
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	transport := sentry.NewHTTPTransport()
	transport.Timeout = timeout

	if err := sentry.Init(sentry.ClientOptions{
		Dsn:              dsn,
		Environment:      cfg.Environment,
		Release:          cfg.Release,
		ServerName:       serverName,
		AttachStacktrace: true,
		Transport:        transport,
		HTTPTransport:    cfg.HTTPTransport,
	}); err != nil {
		return err
	}

	clientMu.Lock()
	activeClient = &sentryClient{
		logger:     sentry.NewLogger(context.Background()),
		timeout:    timeout,
		capture4xx: cfg.Capture4xx,
		capture5xx: cfg.Capture5xx,
	}
	clientMu.Unlock()

	return nil
}

func ShutdownSentry(ctx context.Context) {
	clientMu.Lock()
	sc := activeClient
	activeClient = nil
	clientMu.Unlock()
	if sc == nil {
		return
	}

	timeout := sc.timeout
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining > 0 {
			timeout = remaining
		}
	}
	if timeout <= 0 {
		timeout = 2 * time.Second
	}

	sentry.Flush(timeout)
}

func SentryLogWriter(minLevel string) io.Writer {
	clientMu.RLock()
	sc := activeClient
	clientMu.RUnlock()
	if sc == nil || sc.logger == nil {
		return nil
	}

	return &sentryLogWriter{
		logger:   sc.logger,
		minLevel: normalizeLogLevel(minLevel),
	}
}

func HTTPStatusCaptureMiddleware() fiber.Handler {
	return func(c fiber.Ctx) error {
		err := c.Next()
		if err == nil {
			status := c.Response().StatusCode()
			if status >= fiber.StatusBadRequest {
				captureHTTPStatus(c, status, "fiber_response", "", nil)
			}
		}
		return err
	}
}

func CaptureError(c fiber.Ctx, status int, err error, source string) {
	if err == nil {
		return
	}
	captureHTTPStatus(c, status, source, err.Error(), err)
}

func CapturePanic(c fiber.Ctx, recovered any) {
	if recovered == nil {
		return
	}

	// Capture the stack here, as close to the recovery point as possible.
	// By the time any sub-function runs, additional internal frames will have
	// been added and the panic-origin frames are already gone post-recover().
	stack := debug.Stack()

	event := buildHTTPEvent(
		c,
		fiber.StatusInternalServerError,
		sentry.LevelFatal,
		"fiber_panic",
		fmt.Sprintf("panic recovered: %v", recovered),
		fmt.Errorf("panic recovered: %v", recovered),
		recovered,
		stack,
	)
	if event != nil {
		sentry.CaptureEvent(event)
	}
}

func captureHTTPStatus(c fiber.Ctx, status int, source string, message string, capturedErr error) {
	clientMu.RLock()
	sc := activeClient
	clientMu.RUnlock()
	if sc == nil {
		return
	}

	if status >= 500 && !sc.capture5xx {
		return
	}
	if status >= 400 && status < 500 && !sc.capture4xx {
		return
	}

	if message == "" {
		message = extractResponseMessage(c, status)
	}

	event := buildHTTPEvent(c, status, statusLevel(status), source, message, capturedErr, nil, nil)
	if event != nil {
		sentry.CaptureEvent(event)
	}
}

func buildHTTPEvent(
	c fiber.Ctx,
	status int,
	level sentry.Level,
	source string,
	message string,
	capturedErr error,
	recovered any,
	panicStack []byte,
) *sentry.Event {
	clientMu.RLock()
	sc := activeClient
	clientMu.RUnlock()
	if sc == nil {
		return nil
	}

	event := sentry.NewEvent()
	event.Timestamp = time.Now().UTC()
	event.Level = level
	event.Logger = "yamata-no-orochi"
	event.Message = truncate(message, 2048)
	event.Platform = "go"
	event.Tags["http.status_code"] = fmt.Sprintf("%d", status)
	event.Tags["http.method"] = c.Method()
	event.Tags["error.source"] = source

	route, pathParams := requestRoute(c)
	path := c.Path()
	queryString, queryParams := redactedQuery(c)
	requestID := requestid.FromContext(c)

	// Route templates are intentionally tags: unlike the concrete path, they
	// remain low-cardinality and make it possible to identify the handler from
	// GlitchTip without creating a tag value for every resource ID.
	event.Tags["http.route"] = route
	event.Request = &sentry.Request{
		URL:         requestURL(c),
		Method:      c.Method(),
		QueryString: queryString,
		Headers:     filteredHeaders(c),
		Data:        truncate(string(c.Body()), 4096),
	}
	event.Contexts["request"] = sentry.Context{
		"id":          requestID,
		"path":        path,
		"route":       route,
		"is_complete": hasCompleteHTTPRequest(c),
	}
	if len(pathParams) > 0 {
		event.Contexts["request"]["path_params"] = pathParams
	}
	if len(queryParams) > 0 {
		event.Contexts["request"]["query_params"] = queryParams
	}
	event.Contexts["response"] = sentry.Context{
		"body":        truncate(string(c.Response().Body()), 4096),
		"request_id":  requestID,
		"source":      source,
		"status_code": status,
	}

	event.Transaction = route

	if capturedErr != nil {
		event.SetException(capturedErr, 10)
	}

	if recovered != nil {
		event.Contexts["panic"] = sentry.Context{
			"value": fmt.Sprintf("%v", recovered),
			"stack": truncate(string(panicStack), 8192),
		}
		event.Threads = []sentry.Thread{{
			Crashed: true,
			Current: true,
		}}
	}

	return event
}

type sentryLogWriter struct {
	logger   sentry.Logger
	minLevel sentry.LogLevel
}

func (w *sentryLogWriter) Write(p []byte) (int, error) {
	lines := strings.Split(string(p), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		w.emit(line)
	}
	return len(p), nil
}

func (w *sentryLogWriter) emit(line string) {
	message := stripStdLogPrefix(line)
	if message == "" {
		return
	}

	level, attrs, body := parseLogLine(message)
	if compareLogLevels(level, w.minLevel) < 0 {
		return
	}

	entry := logEntryForLevel(w.logger, level).String("sentry.origin", "stdlib.log")
	for key, value := range attrs {
		entry = addLogAttribute(entry, key, value)
	}
	entry.Emit(body)
}

func parseLogLine(message string) (sentry.LogLevel, map[string]any, string) {
	attrs := map[string]any{}

	var payload map[string]any
	if json.Unmarshal([]byte(message), &payload) == nil {
		body := jsonBody(payload, message)
		var level sentry.LogLevel
		if raw, ok := payload["level"]; ok {
			level = normalizeLogLevel(asString(raw))
		} else {
			level = inferLogLevel(body)
		}
		for key, value := range payload {
			switch key {
			case "level", "message", "msg", "time", "timestamp":
				continue
			default:
				attrs[key] = value
			}
		}
		return level, attrs, body
	}

	return inferLogLevel(message), attrs, message
}

func jsonBody(payload map[string]any, fallback string) string {
	for _, key := range []string{"message", "msg", "error", "event"} {
		if value := asString(payload[key]); value != "" {
			return value
		}
	}
	return fallback
}

func addLogAttribute(entry sentry.LogEntry, key string, value any) sentry.LogEntry {
	switch v := value.(type) {
	case string:
		return entry.String(key, truncate(v, 2048))
	case bool:
		return entry.Bool(key, v)
	case float64:
		if float64(int64(v)) == v {
			return entry.Int64(key, int64(v))
		}
		return entry.Float64(key, v)
	case int:
		return entry.Int(key, v)
	case int64:
		return entry.Int64(key, v)
	default:
		return entry.String(key, truncate(fmt.Sprintf("%v", v), 2048))
	}
}

func logEntryForLevel(logger sentry.Logger, level sentry.LogLevel) sentry.LogEntry {
	switch level {
	case sentry.LogLevelTrace:
		return logger.Trace()
	case sentry.LogLevelDebug:
		return logger.Debug()
	case sentry.LogLevelWarn:
		return logger.Warn()
	case sentry.LogLevelError:
		return logger.Error()
	case sentry.LogLevelFatal:
		return logger.LFatal()
	default:
		return logger.Info()
	}
}

func inferLogLevel(message string) sentry.LogLevel {
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "fatal"):
		return sentry.LogLevelFatal
	case strings.Contains(lower, "panic"),
		strings.Contains(lower, "error"),
		strings.Contains(lower, "failed"):
		return sentry.LogLevelError
	case strings.Contains(lower, "warn"):
		return sentry.LogLevelWarn
	case strings.Contains(lower, "debug"):
		return sentry.LogLevelDebug
	default:
		return sentry.LogLevelInfo
	}
}

func normalizeLogLevel(level string) sentry.LogLevel {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "trace":
		return sentry.LogLevelTrace
	case "debug":
		return sentry.LogLevelDebug
	case "warn", "warning":
		return sentry.LogLevelWarn
	case "error":
		return sentry.LogLevelError
	case "fatal":
		return sentry.LogLevelFatal
	default:
		return sentry.LogLevelInfo
	}
}

func compareLogLevels(a, b sentry.LogLevel) int {
	return logLevelRank(a) - logLevelRank(b)
}

func logLevelRank(level sentry.LogLevel) int {
	switch level {
	case sentry.LogLevelTrace:
		return 0
	case sentry.LogLevelDebug:
		return 1
	case sentry.LogLevelInfo:
		return 2
	case sentry.LogLevelWarn:
		return 3
	case sentry.LogLevelError:
		return 4
	case sentry.LogLevelFatal:
		return 5
	default:
		return 2
	}
}

func stripStdLogPrefix(message string) string {
	return strings.TrimSpace(stdLogPrefixRE.ReplaceAllString(message, ""))
}

func requestURL(c fiber.Ctx) string {
	proto := c.Get("X-Forwarded-Proto")
	if proto == "" {
		proto = "http"
	}

	if !hasCompleteHTTPRequest(c) {
		return ""
	}
	path := c.Path()
	if host := c.Host(); host != "" {
		return fmt.Sprintf("%s://%s%s", proto, host, path)
	}

	// A parser-level error can have a request path but no Host header. Preserve
	// the useful path rather than emitting the misleading "http:///" URL.
	return path
}

func requestRoute(c fiber.Ctx) (string, map[string]string) {
	if !hasCompleteHTTPRequest(c) {
		// Fiber did not finish parsing the request. No application handler can
		// be associated with this event.
		return "<unavailable>", nil
	}

	route := c.Route()
	if route == nil || route.Path == "" || len(route.Handlers) == 0 || c.IsMiddleware() {
		return "<unmatched>", nil
	}

	params := make(map[string]string, len(route.Params))
	for _, name := range route.Params {
		if value := c.Params(name); value != "" {
			params[name] = redactRequestValue(name, value)
		}
	}
	return route.Path, params
}

func hasCompleteHTTPRequest(c fiber.Ctx) bool {
	// fasthttp initializes a fresh request context as GET / with no Host. That
	// is also the shape Fiber gives its error handler after a client times out
	// before sending a complete request, as in the reported 408 events.
	return c.Host() != "" || c.OriginalURL() != "/"
}

func redactedQuery(c fiber.Ctx) (string, map[string][]string) {
	rawQuery := string(c.Request().URI().QueryString())
	if rawQuery == "" {
		return "", nil
	}

	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return "[unparseable]", map[string][]string{"[unparseable]": {"[redacted]"}}
	}

	redactedValues := make(url.Values, len(values))
	queryParams := make(map[string][]string, len(values))
	for name, values := range values {
		redacted := make([]string, len(values))
		for index, value := range values {
			redacted[index] = redactRequestValue(name, value)
		}
		redactedValues[name] = redacted
		queryParams[name] = redacted
	}

	return redactedValues.Encode(), queryParams
}

func redactRequestValue(name, value string) string {
	if isSensitiveRequestField(name) {
		return "[redacted]"
	}
	return truncate(value, 512)
}

func isSensitiveRequestField(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	return strings.Contains(name, "authorization") ||
		strings.Contains(name, "credential") ||
		strings.Contains(name, "password") ||
		strings.Contains(name, "secret") ||
		strings.Contains(name, "token") ||
		strings.Contains(name, "api_key") ||
		strings.Contains(name, "api-key") ||
		strings.Contains(name, "apikey") ||
		strings.Contains(name, "signature")
}

func filteredHeaders(c fiber.Ctx) map[string]string {
	headers := map[string]string{}
	for key, values := range c.GetReqHeaders() {
		value := strings.Join(values, ", ")
		switch strings.ToLower(key) {
		case "authorization", "cookie", "x-api-key":
			headers[key] = "[redacted]"
		default:
			headers[key] = truncate(value, 512)
		}
	}
	return headers
}

func extractResponseMessage(c fiber.Ctx, status int) string {
	if body := strings.TrimSpace(string(c.Response().Body())); body != "" {
		return truncate(body, 2048)
	}
	return fmt.Sprintf("HTTP %d %s", status, http.StatusText(status))
}

func statusLevel(status int) sentry.Level {
	if status >= 500 {
		return sentry.LevelError
	}
	return sentry.LevelWarning
}

func truncate(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit]
}

func asString(value any) string {
	if value == nil {
		return ""
	}
	if s, ok := value.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", value)
}
