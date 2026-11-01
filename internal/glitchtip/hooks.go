package glitchtip

import (
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

const (
	module      = "github.com/ddvk/rmfakecloud/"
	selfPackage = module + "internal/glitchtip."
)

// Frame is one stack frame in Sentry's format.
type Frame struct {
	Function string `json:"function"`
	Module   string `json:"module,omitempty"`
	Filename string `json:"filename"`
	AbsPath  string `json:"abs_path"`
	Lineno   int    `json:"lineno"`
	InApp    bool   `json:"in_app"`
}

// Stacktrace returns the caller's stack, oldest first, without logrus, this
// package and the runtime's panic machinery.
func Stacktrace() []Frame {
	pcs := make([]uintptr, 64)
	n := runtime.Callers(2, pcs)
	frames := runtime.CallersFrames(pcs[:n])
	var out []Frame
	for {
		f, more := frames.Next()
		fn := f.Function
		if !strings.HasPrefix(fn, "github.com/sirupsen/logrus.") &&
			!strings.HasPrefix(fn, selfPackage) &&
			!strings.HasPrefix(fn, "runtime.") {
			pkg, name := splitFunction(fn)
			out = append(out, Frame{
				Function: name,
				Module:   pkg,
				Filename: shortFile(f.File),
				AbsPath:  f.File,
				Lineno:   f.Line,
				InApp:    strings.HasPrefix(fn, module),
			})
		}
		if !more {
			break
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// "github.com/a/b/pkg.(*T).M" -> ("github.com/a/b/pkg", "(*T).M")
func splitFunction(fn string) (string, string) {
	slash := strings.LastIndex(fn, "/")
	dot := strings.Index(fn[slash+1:], ".")
	if dot < 0 {
		return "", fn
	}
	return fn[:slash+1+dot], fn[slash+1+dot+1:]
}

// The path from the module root when the file is in this repo.
func shortFile(path string) string {
	if i := strings.Index(path, "/internal/"); i >= 0 {
		return path[i+1:]
	}
	if i := strings.Index(path, "/cmd/"); i >= 0 {
		return path[i+1:]
	}
	return path
}

// Hook reports logrus entries at Error, Fatal and Panic.
type Hook struct{ C *Client }

// Levels implements logrus.Hook.
func (Hook) Levels() []logrus.Level {
	return []logrus.Level{logrus.PanicLevel, logrus.FatalLevel, logrus.ErrorLevel}
}

// Fire implements logrus.Hook. Fatal and Panic are sent before returning,
// because logrus exits or panics right after its hooks.
func (h Hook) Fire(e *logrus.Entry) error {
	ev := Event{Level: "error", Type: "log.Error", Value: e.Message, Frames: Stacktrace()}
	switch e.Level {
	case logrus.FatalLevel:
		ev.Level, ev.Type = "fatal", "log.Fatal"
	case logrus.PanicLevel:
		ev.Level, ev.Type = "fatal", "log.Panic"
	}
	if len(e.Data) > 0 {
		ev.Extra = make(map[string]any, len(e.Data))
		for k, v := range e.Data {
			ev.Extra[k] = fmt.Sprint(v)
		}
		if err, ok := e.Data[logrus.ErrorKey].(error); ok {
			ev.Type = fmt.Sprintf("%T", err)
		}
	}
	if ev.Level == "fatal" {
		_, _ = h.C.CaptureSync(ev)
		return nil
	}
	h.C.Capture(ev)
	return nil
}

// GinRecovery reports a handler panic with its stack and request, then
// panics again so gin's own Recovery still answers 500 and logs it.
func (c *Client) GinRecovery() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if c == nil {
			ctx.Next()
			return
		}
		defer func() {
			r := recover()
			if r == nil {
				return
			}
			if r != http.ErrAbortHandler {
				c.Capture(Event{
					Level:   "error",
					Type:    "panic",
					Value:   fmt.Sprint(r),
					Frames:  Stacktrace(),
					Request: &Request{Method: ctx.Request.Method, URL: ctx.Request.URL.String()},
				})
			}
			panic(r)
		}()
		ctx.Next()
	}
}

var defaultClient *Client

// Init sets the process-wide client and hooks it into logrus. An empty DSN
// leaves reporting off.
func Init(dsn, release string) error {
	c, err := New(dsn, release)
	if err != nil {
		return err
	}
	defaultClient = c
	if c != nil {
		logrus.AddHook(Hook{C: c})
	}
	return nil
}

// Default is the client Init set, or nil.
func Default() *Client { return defaultClient }

// Close flushes the default client, waiting at most 3 s.
func Close() { defaultClient.Flush(3 * time.Second) }
