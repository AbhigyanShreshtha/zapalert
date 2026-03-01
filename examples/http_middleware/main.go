package main

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/your_github_user_or_org/zapalert"
	"github.com/your_github_user_or_org/zapalert/alert"
	"github.com/your_github_user_or_org/zapalert/ctxmeta"
)

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

var requestCounter atomic.Uint64

func main() {
	log, err := zapalert.New(
		zapalert.WithServiceName("http-demo"),
		zapalert.WithDefaultAlertLevel("NONE"),
		zapalert.WithEscalation(alert.Config{
			Enabled:               true,
			Window:                time.Minute,
			BucketCount:           6,
			DefaultBaseAlertLevel: "P3-Low",
			ErrorBaseAlertLevel:   "P3-Low",
			Ladder:                []alert.AlertLevel{"P3-Low", "P2-Mid", "P1-High"},
			Rules: []alert.Rule{
				{
					MethodPattern:       "^GET /unstable$",
					CountThresholds:     map[alert.AlertLevel]int{"P3-Low": 5, "P2-Mid": 20},
					PercentThresholds:   map[alert.AlertLevel]float64{"P2-Mid": 0.20},
					MinimumRequestCount: 20,
					Cooldown:            2 * time.Minute,
					Deescalate:          true,
				},
			},
		}),
	)
	if err != nil {
		panic(err)
	}
	defer log.Sync()

	mux := http.NewServeMux()
	mux.HandleFunc("/ok", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/unstable", func(w http.ResponseWriter, r *http.Request) {
		if time.Now().UnixNano()%3 == 0 {
			http.Error(w, "upstream timeout", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("unstable-ok"))
	})

	handler := withLogging(log, mux)
	fmt.Println("listening on :8080")
	if err := http.ListenAndServe(":8080", handler); err != nil {
		panic(err)
	}
}

func withLogging(log zapalert.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := fmt.Sprintf("req-%d", requestCounter.Add(1))
		ctx := ctxmeta.WithRequestID(r.Context(), requestID)
		ctx = ctxmeta.WithUserAgent(ctx, r.UserAgent())
		if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
			ctx = ctxmeta.WithIP(ctx, host)
		}

		methodName := r.Method + " " + r.URL.Path
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		started := time.Now()

		next.ServeHTTP(recorder, r.WithContext(ctx))

		success := recorder.status < 500
		log.ObserveRequest(ctx, methodName, success)

		latency := time.Since(started)
		if !success {
			log.Error(
				ctx,
				methodName,
				errors.New(http.StatusText(recorder.status)),
				"request failed",
				zap.Int("status", recorder.status),
				zap.Duration("latency", latency),
			)
			return
		}

		log.Info(ctx, methodName, "request completed", zap.Int("status", recorder.status), zap.Duration("latency", latency))
	})
}
