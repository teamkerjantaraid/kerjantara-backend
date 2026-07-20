package database

import (
	"context"
	"log"
	"os"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func ConnectDB() (*pgxpool.Pool, error) {
	dsn := os.Getenv("DATABASE_URL")
	// contoh: postgres://postgres.xjfddrqebuoatsfbzykl:PASSWORD@aws-0-ap-southeast-1.pooler.supabase.com:6543/postgres?sslmode=require

	if dsn == "" {
		log.Fatal("DATABASE_URL belum di-set")
	}
	log.Printf("Menggunakan DSN: %s", dsn)
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}

	// pooler mode transaction (port 6543) tidak support prepared statement caching
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol

	// Aktifkan query logging jika LOG_QUERIES=true
	if os.Getenv("LOG_QUERIES") == "true" {
		cfg.ConnConfig.Tracer = &QueryTracer{}
		log.Println("Query logging aktif")
	}

	cfg.MaxConns = 10
	cfg.MinConns = 2

	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		return nil, err
	}

	if err := pool.Ping(context.Background()); err != nil {
		return nil, err
	}

	log.Println("Berhasil konek ke database Supabase")
	return pool, nil
}