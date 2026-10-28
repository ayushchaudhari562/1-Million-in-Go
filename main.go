package main
import (
	"bytes"
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

var dbPool *pgxpool.Pool;


var writeQueue = make(chan Item, 200000);
var totalWrites uint64;

func flushBatchToDB(items []Item) {
	if dbPool == nil || len(items) == 0 {
		return;
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second);
	defer cancel();

	rows := make([][]any, len(items));
	for i, it := range items {
		rows[i] = []any{it.ID, it.Code};
	}
	dbPool.CopyFrom(
		ctx,
		pgx.Identifier{"codes"},
		[]string{"id", "code"},
		pgx.CopyFromRows(rows),
	);
}

func startBatchWorker() {
	go func() {
		batch := make([]Item, 0, 5000);
		ticker := time.NewTicker(100 * time.Millisecond);
		for {
			select {
			case item := <-writeQueue:
				batch = append(batch, item);
				if len(batch) >= 5000 {
					flushBatchToDB(batch);
					batch = batch[:0];
				}
			case <-ticker.C:
				if len(batch) > 0 {
					flushBatchToDB(batch);
					batch = batch[:0];
				}
			}
		}
	}();
}

type Item struct {
	ID   int
	Code string
}

const TOTAL_RECORDS = 100000

var memoryStore [TOTAL_RECORDS]string

func generateDummyCode() string {
	bytes := make([]byte, 250);
	rand.Read(bytes);
	return hex.EncodeToString(bytes);
}

func initData() {
	fmt.Println("Pre-loading 100,000 real records into RAM...");
	dummy := generateDummyCode();
	for i := 0; i < TOTAL_RECORDS; i++ {
		memoryStore[i] = dummy;
	}
	fmt.Println("100,000 records ready in RAM (Zero disk/network latency)!");
}

func initDB(){
	dbURL := os.Getenv("DATABASE_URL");
	if dbURL == ""{
		dbURL = "postgresql://postgres:ayush9569518989@localhost:5432/1Million";
		
	}
	config , err := pgxpool.ParseConfig(dbURL);
	if err != nil {
		log.Printf("DB Config error: %v", err);
		return;
	}
	config.MaxConns = 100;
	config.MinConns = 10;
	config.MaxConnIdleTime = 5 * time.Minute;

	ctx , cancel := context.WithTimeout(context.Background(),5*time.Second);
	defer cancel();

	pool, err := pgxpool.NewWithConfig(ctx, config);
	if err != nil {
		log.Printf("DB connection error %v", err);
		return;
	}
	if err := pool.Ping(ctx); err != nil {
		log.Printf("DB Ping error: %v", err);
		return;
	}
	dbPool = pool;
	fmt.Println(" Connected to PostgreSQL (1Million Database)!");

	dbPool.Exec(ctx, `CREATE TABLE IF NOT EXISTS codes (
						id INT PRIMARY KEY,
						code TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS codes_id_idx ON codes(id)`);
	var count int;
	dbPool.QueryRow(ctx, "SELECT count(*) FROM codes").Scan(&count);
	var maxID uint64;
	dbPool.QueryRow(ctx, "SELECT COALESCE(MAX(id), 0) FROM codes").Scan(&maxID);
	atomic.StoreUint64(&totalWrites, maxID);
	fmt.Printf(" PostgreSQL table 'codes' ready with %d rows! Starting write IDs at %d\n", count, maxID+1);
}

var (
	okBytes   = []byte("OK")
	textPlain = []byte("text/plain; charset=utf8")
	pathSimple    = []byte("/simple")      
	pathCodeFast  = []byte("/code-fast")  
	pathCodeWrite = []byte("/code-write")
	pathCodeDB    = []byte("/code-db")
)

func fastHandler(ctx *fasthttp.RequestCtx) {
	path := ctx.Path();

	if ctx.IsGet() {
		if bytes.Equal(path, pathSimple) {
			ctx.Response.Header.SetContentTypeBytes(textPlain);
			ctx.SetStatusCode(fasthttp.StatusOK);
			ctx.SetBody(okBytes);
			return;
		}

		if bytes.Equal(path, pathCodeFast) {
			id := ctx.QueryArgs().GetUintOrZero("id");
			if id <= 0 || id >= TOTAL_RECORDS {
				id = 1;
			}

			code := memoryStore[id];
			ctx.SetContentType("application/json");
			ctx.SetStatusCode(fasthttp.StatusOK);

			b := ctx.Response.Body()[:0];
			b = append(b, `{"id":`...);
			b = strconv.AppendInt(b, int64(id), 10);
			b = append(b, `,"code":"`...);
			b = append(b, code...);
			b = append(b, `"}`...);
			ctx.SetBody(b);
			return;
		}

		if bytes.Equal(path, pathCodeDB) {
			id := ctx.QueryArgs().GetUintOrZero("id");
			if id <= 0 {
				id = 1;
			}

			if dbPool == nil {
				ctx.SetStatusCode(fasthttp.StatusServiceUnavailable);
				ctx.SetBodyString(`{"error":"database not connected"}`);
				return;
			}

			var code string;
			err := dbPool.QueryRow(ctx, "SELECT code FROM codes WHERE id = $1", id).Scan(&code);
			if err != nil {
				ctx.SetStatusCode(fasthttp.StatusNotFound);
				ctx.SetBodyString(`{"error":"record not found"}`);
				return;
			}

			ctx.SetContentType("application/json");
			ctx.SetStatusCode(fasthttp.StatusOK);

			b := ctx.Response.Body()[:0];
			b = append(b, `{"id":`...);
			b = strconv.AppendInt(b, int64(id), 10);
			b = append(b, `,"code":"`...);
			b = append(b, code...);
			b = append(b, `,"source":"postgresql"}`...);
			ctx.SetBody(b);
			return;
		}
	}

	if ctx.IsPost() {
		if bytes.Equal(path, pathCodeWrite) {
			newID := int(atomic.AddUint64(&totalWrites, 1));
			newCode := generateDummyCode();

			if newID < TOTAL_RECORDS {
				memoryStore[newID] = newCode;
			}

			select {
			case writeQueue <- Item{ID: newID, Code: newCode}:
			default:
			}

			ctx.SetContentType("application/json");
			ctx.SetStatusCode(fasthttp.StatusCreated);

			b := ctx.Response.Body()[:0];
			b = append(b, `{"status":"queued","id":`...);
			b = strconv.AppendInt(b, int64(newID), 10);
			b = append(b, '}');
			ctx.SetBody(b);
			return;
		}
	}

	ctx.Error("Not Found", fasthttp.StatusNotFound);
}
	

func main() {
	numCores := runtime.NumCPU()
	runtime.GOMAXPROCS(numCores)
	fmt.Printf(" Server started on :8081 using %d CPU cores & SO_REUSEPORT multi-listeners!\n", numCores)

	initData();
	initDB();
	startBatchWorker();

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
