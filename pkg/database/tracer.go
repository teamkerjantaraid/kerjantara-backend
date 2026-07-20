package database

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
)

// QueryTracer mencatat setiap query SQL ke log standar.
// Aktifkan hanya saat development (LOG_QUERIES=true di .env).
type QueryTracer struct{}

type traceQueryKey struct{}

type traceQueryData struct {
	sql       string
	args      []any
	startTime time.Time
}

func (t *QueryTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	return context.WithValue(ctx, traceQueryKey{}, &traceQueryData{
		sql:       data.SQL,
		args:      data.Args,
		startTime: time.Now(),
	})
}

func (t *QueryTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	qd, ok := ctx.Value(traceQueryKey{}).(*traceQueryData)
	if !ok {
		return
	}
	elapsed := time.Since(qd.startTime)
	if data.Err != nil {
		log.Printf("[SQL ERR] %s | args=%v | dur=%s | err=%v", qd.sql, qd.args, elapsed, data.Err)
	} else {
		log.Printf("[SQL] %s | args=%v | dur=%s", qd.sql, qd.args, elapsed)
	}
}
