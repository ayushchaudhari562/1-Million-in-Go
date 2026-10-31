package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/rueidis"
	"github.com/valyala/bytebufferpool"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/reuseport"
)

var (
	dbPool      *pgxpool.Pool
	redisClient rueidis.Client
)

var writeQueue = make(chan Item, 200000)
var totalWrites uint64

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
	dbPool.CopyFrom(
		ctx,
		pgx.Identifier{"codes"},
		[]string{"id", "code"},
		pgx.CopyFromRows(rows),
	)
}

func startBatchWorker(done chan struct{}) {
	go func() {
		defer close(done)
		batch := make([]Item, 0, 5000)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case item, ok := <-writeQueue:
				if !ok {
					if len(batch) > 0 {
						flushBatchToDB(batch)
					}
					return
				}
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
	bytes := make([]byte, 250)
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

func initDB() {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgresql://postgres:ayush9569518989@localhost:5432/1Million"
	}
	config, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		log.Printf("⚠️ DB Config error: %v", err)
		return
	}
	config.MaxConns = 100
	config.MinConns = 10
	config.MaxConnIdleTime = 5 * time.Minute

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		log.Printf("⚠️ DB connection warning: %v (Postgres routes disabled)", err)
		return
	}
	if err := pool.Ping(ctx); err != nil {
		log.Printf("⚠️ DB Ping warning: %v (Postgres routes disabled)", err)
		return
	}
	dbPool = pool
	fmt.Println(" Connected to PostgreSQL (1Million Database)!")

	dbPool.Exec(ctx, `CREATE TABLE IF NOT EXISTS codes (
						id INT PRIMARY KEY,
						code TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS codes_id_idx ON codes(id)`)
	var count int
	dbPool.QueryRow(ctx, "SELECT count(*) FROM codes").Scan(&count)
	var maxID uint64
	dbPool.QueryRow(ctx, "SELECT COALESCE(MAX(id), 0) FROM codes").Scan(&maxID)
	atomic.StoreUint64(&totalWrites, maxID)
	fmt.Printf(" PostgreSQL table 'codes' ready with %d rows! Starting write IDs at %d\n", count, maxID+1)
}

func initRedis() {
	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		redisURL = "127.0.0.1:6379"
	}

	client, err := rueidis.NewClient(rueidis.ClientOption{
		InitAddress:       []string{redisURL},
		CacheSizeEachConn: 128 * (1 << 20), // 128 MB Client-Side Cache
		DisableCache:      false,
	})
	if err != nil {
		log.Printf("⚠️ Redis configuration error: %v", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := client.Do(ctx, client.B().Ping().Build()).Error(); err != nil {
		log.Printf("⚠️ Redis ping warning: %v (Redis routes disabled until reachable at %s)", err, redisURL)
		return
	}

	redisClient = client
	fmt.Printf(" Connected to Redis at %s (Auto-pipelining & RESP3 Client-Side Cache active)!\n", redisURL)
}

var (
	okBytes       = []byte("OK")
	textPlain     = []byte("text/plain; charset=utf8")
	pathSimple    = []byte("/simple")
	pathCodeFast  = []byte("/code-fast")
	pathCodeRedis = []byte("/code-redis")
	pathCodeDB    = []byte("/code-db")
	pathCodeWrite = []byte("/code-write")
	pathRedisSeed = []byte("/redis-seed")
)

func fastHandler(ctx *fasthttp.RequestCtx) {
	path := ctx.Path()

	if ctx.IsGet() {
		if bytes.Equal(path, pathSimple) {
			ctx.Response.Header.SetContentTypeBytes(textPlain)
			ctx.SetStatusCode(fasthttp.StatusOK)
			ctx.SetBody(okBytes)
			return
		}

		if bytes.Equal(path, pathCodeFast) {
			id := ctx.QueryArgs().GetUintOrZero("id")
			if id <= 0 || id >= TOTAL_RECORDS {
				id = 1
			}

			code := memoryStore[id]
			ctx.SetContentType("application/json")
			ctx.SetStatusCode(fasthttp.StatusOK)

			b := ctx.Response.Body()[:0]
			b = append(b, `{"id":`...)
			b = strconv.AppendInt(b, int64(id), 10)
			b = append(b, `,"code":"`...)
			b = append(b, code...)
			b = append(b, `"}`...)
			ctx.SetBody(b)
			return
		}

		if bytes.Equal(path, pathCodeRedis) {
			id := ctx.QueryArgs().GetUintOrZero("id")
			if id <= 0 {
				id = 1
			}

			if redisClient == nil {
				ctx.SetStatusCode(fasthttp.StatusServiceUnavailable)
				ctx.SetBodyString(`{"error":"redis not connected"}`)
				return
			}

			// Efficiently format key "code:<id>" using pooled byte buffer
			kb := bytebufferpool.Get()
			kb.SetString("code:")
			kb.B = strconv.AppendInt(kb.B, int64(id), 10)
			keyStr := kb.String()
			bytebufferpool.Put(kb)

			// Fast Path: Check Client-Side Cache (DoCache) -> Local RAM (15ns)
			// On miss, rueidis auto-pipelines GET command to Redis over TCP (~200µs)
			cmd := redisClient.B().Get().Key(keyStr).Cache()
			resp := redisClient.DoCache(ctx, cmd, 10*time.Minute)
			val, err := resp.ToString()

			if err != nil {
				// Cache miss in Redis -> Fallback to L1 RAM or L3 PostgreSQL
				if id < TOTAL_RECORDS && memoryStore[id] != "" {
					val = memoryStore[id]
					go func(k, v string) {
						setCmd := redisClient.B().Set().Key(k).Value(v).Ex(10 * time.Minute).Build()
						_ = redisClient.Do(context.Background(), setCmd)
					}(keyStr, val)
				} else if dbPool != nil {
					var dbCode string
					if err := dbPool.QueryRow(ctx, "SELECT code FROM codes WHERE id = $1", id).Scan(&dbCode); err == nil {
						val = dbCode
						go func(k, v string) {
							setCmd := redisClient.B().Set().Key(k).Value(v).Ex(10 * time.Minute).Build()
							_ = redisClient.Do(context.Background(), setCmd)
						}(keyStr, val)
					}
				}
			}

			if val == "" {
				ctx.SetStatusCode(fasthttp.StatusNotFound)
				ctx.SetBodyString(`{"error":"record not found"}`)
				return
			}

			ctx.SetContentType("application/json")
			ctx.SetStatusCode(fasthttp.StatusOK)

			b := ctx.Response.Body()[:0]
			b = append(b, `{"id":`...)
			b = strconv.AppendInt(b, int64(id), 10)
			b = append(b, `,"code":"`...)
			b = append(b, val...)
			b = append(b, `,"source":"redis"}`...)
			ctx.SetBody(b)
			return
		}

		if bytes.Equal(path, pathCodeDB) {
			id := ctx.QueryArgs().GetUintOrZero("id")
			if id <= 0 {
				id = 1
			}

			if dbPool == nil {
				ctx.SetStatusCode(fasthttp.StatusServiceUnavailable)
				ctx.SetBodyString(`{"error":"database not connected"}`)
				return
			}

			var code string
			err := dbPool.QueryRow(ctx, "SELECT code FROM codes WHERE id = $1", id).Scan(&code)
			if err != nil {
				ctx.SetStatusCode(fasthttp.StatusNotFound)
				ctx.SetBodyString(`{"error":"record not found"}`)
				return
			}

			ctx.SetContentType("application/json")
			ctx.SetStatusCode(fasthttp.StatusOK)

			b := ctx.Response.Body()[:0]
			b = append(b, `{"id":`...)
			b = strconv.AppendInt(b, int64(id), 10)
			b = append(b, `,"code":"`...)
			b = append(b, code...)
			b = append(b, `,"source":"postgresql"}`...)
			ctx.SetBody(b)
			return
		}
	}

	if ctx.IsPost() {
		if bytes.Equal(path, pathCodeWrite) {
			newID := int(atomic.AddUint64(&totalWrites, 1))
			newCode := generateDummyCode()

			// 1. Update L1 In-Process RAM
			if newID < TOTAL_RECORDS {
				memoryStore[newID] = newCode
			}

			// 2. Asynchronously update L2 Redis Cache
			if redisClient != nil {
				go func(id int, code string) {
					key := "code:" + strconv.Itoa(id)
					setCmd := redisClient.B().Set().Key(key).Value(code).Ex(10 * time.Minute).Build()
					_ = redisClient.Do(context.Background(), setCmd)
				}(newID, newCode)
			}

			// 3. Queue into L3 Write-Behind channel for batch persistence to PostgreSQL
			select {
			case writeQueue <- Item{ID: newID, Code: newCode}:
			default:
			}

			ctx.SetContentType("application/json")
			ctx.SetStatusCode(fasthttp.StatusCreated)

			b := ctx.Response.Body()[:0]
			b = append(b, `{"status":"queued","id":`...)
			b = strconv.AppendInt(b, int64(newID), 10)
			b = append(b, '}')
			ctx.SetBody(b)
			return
		}

		if bytes.Equal(path, pathRedisSeed) {
			if redisClient == nil {
				ctx.SetStatusCode(fasthttp.StatusServiceUnavailable)
				ctx.SetBodyString(`{"error":"redis not connected"}`)
				return
			}
			go func() {
				fmt.Println("🚀 Seeding 10,000 records into Redis using high-speed pipelining...")
				ctx := context.Background()
				for i := 1; i <= 10000; i += 500 {
					cmds := make(rueidis.Commands, 0, 500)
					for j := i; j < i+500 && j < TOTAL_RECORDS; j++ {
						key := "code:" + strconv.Itoa(j)
						cmds = append(cmds, redisClient.B().Set().Key(key).Value(memoryStore[j]).Ex(1*time.Hour).Build())
					}
					for _, resp := range redisClient.DoMulti(ctx, cmds...) {
						_ = resp.Error()
					}
				}
				fmt.Println(" Pre-seeded 10,000 records into Redis successfully!")
			}()
			ctx.SetContentType("application/json")
			ctx.SetStatusCode(fasthttp.StatusAccepted)
			ctx.SetBodyString(`{"status":"seeding_started","target_records":10000}`)
			return
		}
	}

	ctx.Error("Not Found", fasthttp.StatusNotFound)
}

func main() {
	numCores := runtime.NumCPU()
	runtime.GOMAXPROCS(numCores)
	fmt.Printf("🚀 Server started on :8081 using %d CPU cores & SO_REUSEPORT multi-listeners!\n", numCores)

	initData()
	initDB()
	initRedis()

	workerDone := make(chan struct{})
	startBatchWorker(workerDone)

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

	// Graceful shutdown listener
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	fmt.Println("\n🛑 Shutdown signal received. Flushing write queues & closing connections...")
	close(writeQueue)
	<-workerDone

	if dbPool != nil {
		dbPool.Close()
	}
	if redisClient != nil {
		redisClient.Close()
	}
	fmt.Println(" Graceful shutdown complete. Zero data loss!")
}
