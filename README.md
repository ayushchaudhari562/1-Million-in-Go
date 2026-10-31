# 1 Million RPS in Go: Architecture, Benchmarks & Deep Internals

A comprehensive technical study and benchmark progression comparing **Node.js** vs **Go (Golang)** on identical 10-Core (16-Thread) hardware, scaling from baseline to **1,017,103 Requests/Second (1 Million+ RPS)**.

---

## 1. Verified Benchmark Progression (Node.js vs Go)

Every benchmark below was recorded live on identical hardware:

| Milestone / Stage | Architecture / Route | OS & Network Layer | Avg Latency | P50 Latency | Req / Sec (RPS) | Bandwidth / Sec | Total in 10s | Key Architectural Discovery |
| :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- |
| **Node.js Baseline** | `GET /simple` (1 Core) | Windows Host (Loopback) | 0.12 ms | 0.10 ms | **32,680 RPS** | 4.3 MB/s | 327K | V8 JIT + libuv single-threaded event loop |
| **Node.js Multi-Core**| `GET /simple` (16 PM2) | Windows Host (PM2 Cluster)| 1.82 ms | 1.50 ms | **104,218 RPS** | 12.3 MB/s | 1.04M | 16 separate Node processes, 800MB RAM, IPC bottleneck |
| **Node.js DB Read** | `GET /code-v3` | PostgreSQL B-Tree Index | 2.01 ms | 1.95 ms | **7,509 RPS** | 4.1 MB/s | 75K | Pool limit (20 conns) + NVMe SSD latency |
| **Go DB Read** | `GET /code-db` | PostgreSQL B-Tree (`codes` 178K)| 17.53 ms | 9.00 ms | **56,263 RPS** (Peak 79.3K)| 36.5 MB/s | **562K in 10s** | Go `pgxpool` binary wire format vs Node 7.5K (7.5x faster!) |
| **Node.js Redis Read**| `GET /code-fast` | Redis 8 Multi-Thread | 2.07 ms | 2.00 ms | **48,062 RPS** | 35.1 MB/s | 480K | TCP network socket roundtrip (~0.2ms) |
| **Go Milestone 1** | `GET /simple` (10 Cores) | Windows (Standard `net/http`)| 4.64 ms | 3.00 ms | **196,480 RPS** (Peak 203K)| 23.2 MB/s | 1.96M | 1 Single Binary, 15MB RAM, GMP Work-Stealing |
| **Go Milestone 2** | `GET /simple` (10 Cores) | Windows (`fasthttp` Pool) | 4.21 ms | 4.00 ms | **216,755 RPS** (Peak 231K)| 30.3 MB/s | 2.16M | Zero memory allocations (`sync.Pool`) |
| **Go Milestone 3** | `GET /code-fast?id=42` | Windows (In-Process RAM) | 4.76 ms | 5.00 ms | **194,265 RPS** (Peak 201K)| **126.0 MB/s** | **1.94M (1.26 GB!)**| O(1) Contiguous RAM Lookup (15ns) serving 500-char JSON |
| **Go Milestone 4** | `POST /code-write` | Windows (Write-Behind + PG COPY)| 3.84 ms | 4.00 ms | **236,467 RPS** (Peak 280K)| 34.1 MB/s | **1.18M in 5s** | Non-blocking `chan Item` + PG binary `COPY` (115K rows persisted!) |
| **Go Milestone 5** | `GET /simple` (Native Linux)| Kali Linux (Pure Epoll) | 0.43 ms | 144.00 us! | **843,549.64 RPS** | 112.63 MB/s| 8.44M in 10s | Zero Hyper-V overhead, C-based `wrk` load generation |
| **Go Milestone 6** | `GET /simple` (10 Threads) | **Kali Linux (SO_REUSEPORT)** | **0.39 ms** | **121.00 us!** | **1,017,103.27 RPS!** | **113.49 MB/s**| **10.18M in 10s!** | **Multi-Core `SO_REUSEPORT` on 10 Threads + Zero-allocation byte pipeline** |

---

## 2. Milestone 6: The 1,017,103 RPS (1 Million+) Milestone Verification

![wrk 1 Million RPS Live Benchmark in Kali Linux](./img/wrk_1m_benchmark.png)

### Key Milestone 6 Metrics (10 Threads, 200 Connections):
* **Throughput:** **1,017,103.27 Requests / Second** (Over 10.17 Lakh RPS!)
* **P50 Latency:** **121.00 Microseconds (0.121 ms)**
* **Average Latency:** **389.70 Microseconds (0.39 ms)**
* **P90 Latency:** **796.00 Microseconds (0.79 ms)**
* **P99 Latency:** **4.63 milliseconds**
* **Total Processed:** **10,185,622 Requests in 10.01 seconds** (1.11 GB transferred at 113.49 MB/s)

---

## 2.1. Milestone 5: The 843,549 RPS Milestone Verification

![wrk 843K RPS Live Benchmark in Kali Linux](./img/wrk_843k_benchmark.png)

### Key Milestone 5 Metrics:
* **Throughput:** **843,549.64 Requests / Second** (Near 8.5 Lakh RPS!)
* **P50 Latency:** **144.00 Microseconds (0.144 ms)**
* **Average Latency:** **0.43 milliseconds (430 microseconds)**
* **P90 Latency:** **0.87 milliseconds**
* **Total Processed:** **8,444,880 Requests in 10.01 seconds** (1.10 GB transferred)

---

## 3. High-Level Visual Architecture & Tiered Speed Hierarchy

![1 Million RPS High-Performance Go System Architecture Infographic](./img/system_architecture_infographic.png)

### Key Architectural Concepts Visualized:
1. **The 1M Pipeline:** 1,000,000 incoming requests/second distributed directly across 16 parallel CPU cores via `SO_REUSEPORT` hardware socket queues.
2. **The 3-Tier Storage Hierarchy:**
   * **L1 In-Process RAM:** 15 Nanoseconds (O(1) memory lookup) powering 1M+ RPS.
   * **L2 Redis Cache:** 200 Microseconds distributed cache over TCP.
   * **L3 PostgreSQL NVMe Database:** Permanent storage handling 56K reads and 236K batch writes.
3. **The Zero-Wait Write-Behind Pattern:**
   * **Fast Path (Green):** User gets instant `< 1ms` response (`201 Created`).
   * **Background Path (Blue):** In-memory channels accumulate 5,000-item batches and stream them into PostgreSQL using binary `COPY` protocol.

---

## 3.1. Node.js PM2 Cluster vs Go GMP Model

![Software Architecture Comparison: Node.js PM2 Cluster vs Golang GMP Model](./img/architecture.jpg)

![Node.js PM2 Cluster vs Go GMP Scheduler Comparison](./img/pm2_vs_gmp.jpg)

---

## 4. Why the Server Peaked at 200K on Windows vs 843K on Linux

When our Go server ran on Windows, throughput hit a ceiling around 216K - 231K RPS. When moved to native Linux with `wrk`, it rocketed to **843K RPS with 144-microsecond latency**. Here is the low-level systems engineering reason:

### 1. Windows Loopback & Autocannon Contention
* On Windows, running **Autocannon** (Node.js) on the same machine consumed **4 to 5 CPU cores** purely generating requests and serializing JS objects. This starved the server of CPU resources.
* Furthermore, Windows Winsock loopback has higher kernel context-switch latency compared to Linux Unix Domain Sockets and Epoll.

### 2. The Hyper-V Virtual Switch Bottleneck
* When we ran `wrk` from inside WSL2 targeting the Windows host (`http://172.24.224.1:8081`), packets had to traverse the **Hyper-V NAT Virtual Network Adapter**.
* Every single packet underwent virtualized network address translation (NAT), adding network jitter.

### 3. The Pure Linux Epoll Advantage (`127.0.0.1`)
* Running both the Go binary (`server_linux`) and `wrk` natively inside Kali Linux allowed all traffic to travel over the **Linux Loopback Interface (`lo`)**.
* Linux Loopback operates directly inside kernel memory buffers without physical network card serialization.
* Combined with Go's native Linux `epoll` netpoller and `wrk`'s C-based event engine, context-switch latency dropped from **4.2 milliseconds down to 144 microseconds (a 30x reduction!)**, unleashing **843,549 RPS**!

---

## 5. The Cross-Compilation Magic: Building `server_linux`

We did not need to install the Go compiler or dependencies inside Kali Linux. Go includes a built-in cross-compiler that can produce self-contained, statically linked Linux ELF binaries directly from Windows!

### How to Compile `server_linux` from Windows:
Run the following command in Windows Command Prompt:
```cmd
set GOOS=linux&& set GOARCH=amd64&& go build -o server_linux main.go
```
* **`GOOS=linux`**: Instructs the Go compiler to target the Linux Operating System kernel ABI.
* **`GOARCH=amd64`**: Targets 64-bit x86 architecture.
* **Static Binary**: The resulting `server_linux` file (~8.4 MB) has zero external C library dependencies (`glibc` or dynamic linkers). It runs out-of-the-box on any Linux distro (Kali, Ubuntu, Debian, Alpine, Arch).

---

## 6. How to Deploy & Run the Server in Kali Linux (WSL2)

### Step 1: Compile Linux Binary from Windows PowerShell
Run this command in PowerShell to cross-compile the high-performance Linux binary:
```powershell
$env:GOOS="linux"; $env:GOARCH="amd64"; go build -o server_linux main.go
```

### Step 2: Ensure Port 8081 is Free
Make sure no server is holding port `8081` on Windows (`Ctrl + C` in any running terminal).

### Step 3: Run the Official 1 Million RPS Benchmark in Kali Linux (WSL2)
Copy and paste this single command into your **Kali Linux** terminal. It copies the binary, tunes socket descriptors (`ulimit -n 65535`), launches the multi-core `SO_REUSEPORT` daemon, executes `wrk` with 9 threads & 250 connections, and cleans up:

```bash
ulimit -n 65535; cp /mnt/c/Users/LOQ/Desktop/Golang/1\ Million\ in\ Go/server_linux ~/server_linux && chmod +x ~/server_linux && GOGC=off ~/server_linux & sleep 1; wrk -t9 -c250 -d10s --latency http://127.0.0.1:8081/simple; pkill -9 -f server_linux
```

### Step 4: Benchmark Real 500-Character JSON Read (`/code-fast`)
To benchmark zero-allocation real JSON responses reading contiguous RAM at scale:

```bash
ulimit -n 65535; cp /mnt/c/Users/LOQ/Desktop/Golang/1\ Million\ in\ Go/server_linux ~/server_linux && chmod +x ~/server_linux && GOGC=off ~/server_linux & sleep 1; wrk -t9 -c250 -d10s --latency "http://127.0.0.1:8081/code-fast?id=42"; pkill -9 -f server_linux
```

### Step 5: Benchmark Distributed L2 Redis Read (`/code-redis`)
To test the L2 Distributed Cache powered by `rueidis` auto-pipelining & RESP3 client-side caching:

1. **Start Redis Server** (if not already running):
```bash
sudo service redis-server start
# or: redis-server --daemonize yes
```

2. **Pre-seed Redis with 10,000 Keys** (via pipelined background worker):
```bash
curl -X POST http://127.0.0.1:8081/redis-seed
```

3. **Run High-Concurrency Benchmark**:
```bash
wrk -t9 -c250 -d10s --latency "http://127.0.0.1:8081/code-redis?id=42"
```

---

## 7. Deep-Dive: Core Engineering Concepts

### A. The Latency Hierarchy (Why In-Process RAM Crushed Redis & Postgres)

![Multi-Tier Storage and Hardware Latency Hierarchy](./img/latency_hierarchy.jpg)

Computer hardware operates across vastly different timescales:

```
[ CPU L1/L2 Cache ] ──► 1 Nanosecond       (Silicon Registers)
[ Local RAM ]       ──► 15 - 50 Nanoseconds (O(1) Array Lookup - 200K+ RPS)
[ Redis (Network) ] ──► 200 - 500 Microseconds (10,000x slower than RAM)
[ PostgreSQL (SSD)] ──► 1 - 5 Milliseconds   (100,000x slower than RAM!)
```

* **In PostgreSQL**: Each query requires connection pool checkout, SQL parsing, and SSD write-ahead log sync (`fsync`). With 20 connections taking 2ms each, the theoretical ceiling is `20 / 0.002 = 10,000 RPS`.
* **In Redis**: While Redis stores data in RAM, reading over a TCP socket adds a network roundtrip overhead (~0.2ms) and socket buffer parsing.
* **In Go In-Process RAM (`memoryStore`)**: Data lives inside the Go process's virtual memory address space. Reading an element takes **15 nanoseconds** with zero network hops, delivering **194,265 RPS** while streaming **1.26 GB of JSON in 10 seconds**!

---

### B. Non-Blocking Write-Behind Pattern (Goroutine Channels)

![Non-Blocking Write-Behind Pattern using Golang Channels](./img/write_behind.jpg)

* **User Zero-Wait**: The incoming `POST /code-write` request stores data directly into in-memory RAM in ~0.05ms, queues the task into an in-memory channel (`chan Item, 200000`), and returns `201 Created` in **< 1ms**.
* **Zero Network Hop Queue**: Unlike Node.js which required `redis.lpush` over TCP, Go Channels pass raw memory pointers across CPU cores at nanosecond speeds.
* **Batch Flushing**: Background Goroutines consume from the channel and flush records in batches of 5,000 directly into PostgreSQL using binary `COPY` streaming (`pgx.CopyFrom`), reducing disk I/O operations by **98%**.
* **Verified Result**: **236,467 Sustained Writes/Second (Peak: 280,575 RPS)** with only 3.84ms average latency, persisting **115,026 new rows** into PostgreSQL table `codes` in 5 seconds!

---

### C. Zero-Allocation Buffer Pooling (`fasthttp` vs Standard `net/http`)
Standard `net/http` allocates new memory on the heap for every single request:
* At 200,000 RPS, generating 2KB of structs per request produces **400 MB of garbage memory every second**.
* The Go Garbage Collector (GC) must frequently pause execution to clean up heap memory, causing latency jitter.
* `fasthttp` eliminates this via **`sync.Pool`**: When a request completes, its `RequestCtx` buffer is not freed; it is returned to an in-memory pool and immediately recycled for the next incoming request.
* **Result**: Zero heap allocations, reducing latency standard deviation from **5.15 ms down to 2.06 ms**!

---

### D. The 1 Million RPS Milestone: Key Optimizations & Deep Internals

To breach **1,000,000 Requests/Second**, the following low-level systems optimizations were implemented:

#### 1. Multi-Core `SO_REUSEPORT` Architecture
* **The Problem:** Standard servers run a single `net.Listener` on port 8081. When 16 CPU cores handle requests simultaneously, the Linux kernel enforces a single spinlock on `accept()`, causing core idling.
* **The Optimization:** Using `github.com/valyala/fasthttp/reuseport`, we open **independent socket file descriptors** bound to port 8081. The Linux kernel TCP/IP stack hashes incoming connections directly to individual core queues, eliminating lock contention.

![Linux Kernel Load Balancing (SO_REUSEPORT) vs External Load Balancer](./img/kernel_load_balancer_white.png)

#### 2. Zero-Allocation Binary Path Routing (`bytes.Equal`)
* **The Problem:** Writing `switch string(ctx.Path())` forces Go to allocate a new immutable heap string from socket buffer bytes on every request (10 million allocations in 10s!).
* **The Optimization:** Pre-allocating route byte slices (`pathSimple = []byte("/simple")`) and comparing directly via `bytes.Equal(path, pathSimple)` ensures **0 heap allocations**.

#### 3. Bypassing Runtime Reflection (`fmt.Fprintf` ➔ `strconv.AppendInt`)
* **The Problem:** `fmt.Fprintf(ctx, "...", id, code)` takes `...any` interface arguments. It invokes runtime reflection (`reflect.ValueOf`) to check data types, causing heavy heap boxing allocations.
* **The Optimization:** Writing directly into the recycled response buffer (`b := ctx.Response.Body()[:0]`) and using `strconv.AppendInt(b, int64(id), 10)` converts numbers to ASCII bytes directly in CPU registers without memory allocation.

#### 4. Zero-Copy Query Parameter Parsing
* **The Problem:** Parsing query params via `strconv.Atoi(string(idBytes))` forces a string copy of query bytes.
* **The Optimization:** Using `ctx.QueryArgs().GetUintOrZero("id")` reads the integer directly from the socket byte buffer.

#### 5. Header Stripping
* Disabled `Server: 1M-RPS-Server` response headers (`NoDefaultServerHeader: true`) to save byte formatting cycles across 10 million responses.

---

### E. The Database Paradox: Why Synchronous Read was 56K but Write was 236K RPS

In classical databases, reads are usually faster than writes because reads avoid disk syncs. Yet in our benchmarks:
* **PostgreSQL Direct Read (`GET /code-db`):** **56,263 RPS**
* **PostgreSQL Batch Write (`POST /code-write`):** **236,467 RPS (4x Faster!)**

Here is the architectural reason visualized:

![Database Paradox: Synchronous Read vs Asynchronous Write-Behind Queue](./img/db_read_write_white.png)

#### 1. Synchronous Read (The User Waits for the SSD)
* The client **requires the actual data** to render the UI.
* The connection must stay open across the entire network roundtrip and B-Tree index lookup on disk (~9-17ms P50 latency).
* Max Throughput Ceiling: `100 conns / 0.002s = ~50,000 to 56,263 RPS`.

#### 2. Asynchronous Write-Behind (Zero-Wait User Queue)
* The client **does not wait for PostgreSQL or the SSD**.
* The server accepts the write into an in-process RAM channel in **< 1ms** and returns immediately with `201 Created`.
* A single background worker flushes records in **5,000-item binary batches** via `pgx.CopyFrom`, avoiding single-row transaction commits.
* **Result**: **236,467 Sustained Writes/Second** persisting 115,026 rows in 5 seconds!

---

## 8. Summary of All Milestone Achievements

```
[ Node.js Baseline: 32,680 RPS ]
       │
       ▼ (16 PM2 Workers Cluster)
[ Node.js Maxed Out: 104,218 RPS ]
       │
       ▼ (Node.js DB Read: 7,509 RPS)
[ Go DB Read: 56,263 RPS (7.5x faster than Node!) ]
       │
       ▼ (Go Milestone 1: Standard net/http Baseline)
[ Go Standard Library: 196,480 RPS ]
       │
       ▼ (Go Milestone 2: fasthttp Zero-Allocation)
[ Go fasthttp Windows: 216,755 RPS (Peak 231K) ]
       │
       ▼ (Go Milestone 3: Real 500-Char JSON Read)
[ Go In-Process RAM: 194,265 RPS (1.26 GB read in 10s) ]
       │
       ▼ (Go Milestone 4: Write-Behind + PostgreSQL Persistence)
[ Go Channels Queue: 236,467 Writes/Sec (Peak 280K | 115K rows persisted) ]
       │
       ▼ (Go Milestone 5: Native Linux Epoll + wrk)
[ Go Epoll Baseline: 843,549 RPS ]
       │
       ▼ (Go Milestone 6: SO_REUSEPORT + Zero-Allocation Pipeline on 10 Threads)
[ RECORD: 1,017,103.27 RPS | 121us P50 Latency | 10.18M Requests in 10.01s! ]
```

---

## 9. Multi-Tier Caching & Enterprise Architecture Status

![Enterprise Multi-Tier Caching & Load Balancing Architecture](./img/roadmap_architecture_white.png)

1. **L2 Distributed Cache (Implemented with `rueidis`):**
   * High-throughput automatic pipelining & RESP3 Client-Side Caching (128 MB local cache).
   * **Cache-Aside Read (`GET /code-redis`):** L1 RAM $\rightarrow$ L2 Redis $\rightarrow$ L3 PostgreSQL fallback.
   * **Pipelined Seeder (`POST /redis-seed`):** Pre-warms Redis with 10,000 records in pipelined chunks.
   * **Write-Through (`POST /code-write`):** Concurrently updates L1 RAM, L2 Redis, and L3 channel queue.

2. **Graceful Shutdown & Zero Data Loss (Implemented):**
   * Traps `SIGINT` (`Ctrl+C`) and `SIGTERM`.
   * Safely closes the 200,000-slot write channel, drains remaining queued items via `pgx.CopyFrom`, and closes Postgres/Redis pools cleanly without dropping a single write.

3. **Upcoming Milestone: Reverse Proxy Load Balancer:**
   * Benchmarking Nginx / HAProxy / Envoy upstream proxy overhead against our bare-metal 1M RPS engine.

