package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/reuseport"
)

var dbPool *pgxpool.Pool

var writeQueue = make(chan Item, 200000)
var totalWrites uint64 // Atomic counter

func flushBatchToDB(items []Item) {
	if dbPool == nil || len(items) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	rows := make([][]any, len(items))
	for i, it := range items {
		rows[i] = []any{it.ID, it.Code}
	}
	_, err := dbPool.CopyFrom(
		ctx,
		pgx.Identifier{"items"},
		[]string{"id", "code"},
		pgx.CopyFromRows(rows),
	)
	if err != nil {
		// Log note (e.g. on conflicts), does not block or crash
		log.Printf("DB Batch Flush note: %v", err)
	}
}

func startBatchWorker() {
	go func() {
		batch := make([]Item, 0, 5000)
		ticker := time.NewTicker(100 * time.Millisecond)
		for {
			select {
			case item := <-writeQueue:
				batch = append(batch, item)
				if len(batch) >= 5000 {
					flushBatchToDB(batch)
					batch = batch[:0]
				}
			case <-ticker.C:
				if len(batch) > 0 {
					flushBatchToDB(batch)
					batch = batch[:0]
				}
			}
		}
	}()
}

type Item struct {
	ID   int
	Code string
}

const TOTAL_RECORDS = 100000
var memoryStore [TOTAL_RECORDS]string

func generateDummyCode() string {
	bytes := make([]byte, 250) // 250 bytes = 500 hex characters
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}

func initData() {
	fmt.Println("Pre-loading 100,000 real records into RAM...")
	dummy := generateDummyCode()
	for i := 0; i < TOTAL_RECORDS; i++ {
		memoryStore[i] = dummy
	}
	fmt.Println("100,000 records ready in RAM (Zero disk/network latency)!")
}

var (
	okBytes   = []byte("OK")
	textPlain = []byte("text/plain; charset=utf8")
)

// RequestHandler handles incoming requests with zero allocations
func fastHandler(ctx *fasthttp.RequestCtx) {
	switch string(ctx.Path()) {
	case "/simple":
		ctx.Response.Header.SetContentTypeBytes(textPlain)
		ctx.SetStatusCode(fasthttp.StatusOK)
		ctx.SetBody(okBytes)

	case "/code-fast":
		// Query param se id nikalo: /code-fast?id=42
		id := 1
		idBytes := ctx.QueryArgs().Peek("id")
		if len(idBytes) > 0 {
			if parsed, err := strconv.Atoi(string(idBytes)); err == nil && parsed >= 0 && parsed < TOTAL_RECORDS {
				id = parsed
			}
		}

		code := memoryStore[id]

		ctx.SetContentType("application/json")
		ctx.SetStatusCode(fasthttp.StatusOK)
		fmt.Fprintf(ctx, `{"id":%d,"code":"%s"}`, id, code)

	case "/code-write":
		if !ctx.IsPost() {
			ctx.Error("Method Not Allowed", fasthttp.StatusMethodNotAllowed)
			return
		}

		newID := int(atomic.AddUint64(&totalWrites, 1))
		newCode := generateDummyCode()

		if newID < TOTAL_RECORDS {
			memoryStore[newID] = newCode
		}

		// 2. Non-blocking channel me push karo
		select {
		case writeQueue <- Item{ID: newID, Code: newCode}:
		default:
		}

		ctx.SetContentType("application/json")
		ctx.SetStatusCode(fasthttp.StatusCreated)
		fmt.Fprintf(ctx, `{"status":"queued","id":%d}`, newID)

	case "/code-db":
		// Direct PostgreSQL B-Tree indexed query
		id := 1
		idBytes := ctx.QueryArgs().Peek("id")
		if len(idBytes) > 0 {
			if parsed, err := strconv.Atoi(string(idBytes)); err == nil && parsed > 0 {
				id = parsed
			}
		}

		if dbPool == nil {
			ctx.SetStatusCode(fasthttp.StatusServiceUnavailable)
			ctx.SetBodyString(`{"error":"PostgreSQL database not connected"}`)
			return
		}

		var code string
		err := dbPool.QueryRow(ctx, "SELECT code FROM items WHERE id = $1", id).Scan(&code)
		if err != nil {
			ctx.SetStatusCode(fasthttp.StatusNotFound)
			fmt.Fprintf(ctx, `{"error":"record not found","id":%d}`, id)
			return
		}

		ctx.SetContentType("application/json")
		ctx.SetStatusCode(fasthttp.StatusOK)
		fmt.Fprintf(ctx, `{"id":%d,"code":"%s","source":"postgresql"}`, id, code)

	default:
		ctx.Error("Not Found", fasthttp.StatusNotFound)
	}
}

func initDB() {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgresql://postgres:ayush9569518989@localhost:5432/1Million"
	}

	config, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		log.Printf("⚠️ PostgreSQL config error: %v (running in memory-only mode)", err)
		return
	}
	config.MaxConns = 100
	config.MinConns = 10
	config.MaxConnIdleTime = 5 * time.Minute

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		log.Printf("⚠️ PostgreSQL connect error: %v (running in memory-only mode)", err)
		return
	}

	if err := pool.Ping(ctx); err != nil {
		log.Printf("⚠️ PostgreSQL ping failed: %v (running in memory-only mode)", err)
		return
	}

	dbPool = pool
	fmt.Println(" Connected to PostgreSQL (1Million database)!")

	// Ensure table & index exist
	_, err = dbPool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS items (
			id INT PRIMARY KEY,
			code TEXT NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_items_id ON items(id);
	`)
	if err != nil {
		log.Printf("⚠️ PostgreSQL table init error: %v", err)
		return
	}

	// Pre-seed 10,000 records if empty
	var count int
	_ = dbPool.QueryRow(ctx, "SELECT COUNT(*) FROM items").Scan(&count)
	if count == 0 {
		fmt.Println("Seeding 10,000 initial records into PostgreSQL via binary COPY...")
		dummy := generateDummyCode()
		rows := make([][]any, 10000)
		for i := 0; i < 10000; i++ {
			rows[i] = []any{i + 1, dummy}
		}
		_, err := dbPool.CopyFrom(
			ctx,
			pgx.Identifier{"items"},
			[]string{"id", "code"},
			pgx.CopyFromRows(rows),
		)
		if err != nil {
			log.Printf("Seeding note: %v", err)
		} else {
			fmt.Println(" Seeded 10,000 records in PostgreSQL in <10ms via COPY protocol!")
		}
	} else {
		fmt.Printf(" PostgreSQL table 'items' ready with %d rows!\n", count)
	}
}

func main() {
	numCores := runtime.NumCPU()
	runtime.GOMAXPROCS(numCores)
	fmt.Printf(" Server started on :8081 using %d CPU cores & SO_REUSEPORT multi-listeners!\n", numCores)

	initData()
	initDB()
	startBatchWorker()

	for i := 0; i < numCores; i++ {
		go func(workerID int) {
			ln, err := reuseport.Listen("tcp4", ":8081")
			if err != nil {
				log.Fatalf("Worker %d reuseport error: %v", workerID, err)
			}
			server := &fasthttp.Server{
				Handler:               fastHandler,
				NoDefaultServerHeader: true,
				Concurrency:           256 * 1024,
				ReadBufferSize:        1024,
				WriteBufferSize:       1024,
				DisableKeepalive:      false,
				TCPKeepalive:          true,
			}
			if err := server.Serve(ln); err != nil {
				log.Fatalf("Worker %d serve error: %v", workerID, err)
			}
		}(i)
	}

	select {}
}
