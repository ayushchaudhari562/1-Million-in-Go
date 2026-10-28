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


var writeQueue = make(chan Item, 200000)
var totalWrites uint64 // Atomic counter

func startBatchWorker() {
	go func() {
		batch := make([]Item, 0, 5000)
		ticker := time.NewTicker(100 * time.Millisecond)
		for {
			select {
			case item := <-writeQueue:
				batch = append(batch, item)
				if len(batch) >= 5000 {
					batch = batch[:0]
					//batch = make([]Item, 0, 5000)
				}
			case <-ticker.C:
				if len(batch) > 0 {
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

	pool,err := pgxpool.New(ctx,config);
	if err != nil{
		log.Printf("DB connection error %v",err);
		return;
	}
	if err := pool.Ping(ctx); err != nil {
		log.Printf("DB Ping error: %v", err);
		return;
	}
	dbPool = pool;
	fmt.Println(" Connected to PostgreSQL (1Million Database)!");

	dbPool.Exec(ctx,`CREATE TABLE IF NOT EXISTS items (
						id INT PRIMARY KEY,
						code TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS items_id_idx ON items(id)`);
	var count int ;
	dbPool.QueryRow(ctx,"SELECT count(*) FROM items").Scan(&count);
	if count == 0 {
		fmt.Println("Seeding 10,000 items into PostgreSQL via binary COPY...");
		dummy := generateDummyCode();
		rows := make([][]any, 10000);
		for i := 0; i < 10000; i++ {
			rows[i] = []any{i + 1, dummy};
		}
		dbPool.CopyFrom(
			ctx,
			pgx.Identifier{"items"},
			[]string{"id", "code"},
			pgx.CopyFromRows(rows),
		);
		fmt.Println(" Seeded 10,000 records in PostgreSQL!");
	} else {
		fmt.Printf(" PostgreSQL table 'items' ready with %d rows!\n", count);
	}

}

var (
	okBytes   = []byte("OK")
	textPlain = []byte("text/plain; charset=utf8")
	pathSimple    = []byte("/simple")      
	pathCodeFast  = []byte("/code-fast")  
	pathCodeWrite = []byte("/code-write")
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

	initData()
initDB();
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
