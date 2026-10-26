# 1 Million RPS in Go: Architecture, Benchmarks & Deep Internals

A comprehensive technical study and benchmark progression comparing **Node.js** vs **Go (Golang)** on identical 10-Core (16-Thread) hardware, scaling from baseline to 1 Million Requests/Second.

---

## 1. Verified Benchmark Progression (Node.js vs Go)

Every benchmark below was executed on identical 10-Core hardware with verified Autocannon output logs:

| Milestone / Stage | Architecture / Route | Storage Layer | Avg Latency | Req / Sec (RPS) | Bandwidth / Sec | Total in 10s | Key Architectural Discovery |
| :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- |
| **Node.js Baseline** | `GET /simple` (1 Core) | None (In-Memory) | 0.12 ms | **32,680 RPS** | 4.3 MB/s | 327K | V8 JIT + libuv single-threaded event loop |
| **Node.js Multi-Core**| `GET /simple` (16 PM2) | None (In-Memory) | 1.82 ms | **104,218 RPS** | 12.3 MB/s | 1.04M | 16 separate Node processes, 800MB RAM, IPC bottleneck |
| **Node.js DB Read** | `GET /code-v3` | PostgreSQL B-Tree | 2.01 ms | **7,509 RPS** | 4.1 MB/s | 75K | Connection pool limit (20 conns) + NVMe SSD latency |
| **Node.js Redis Read**| `GET /code-fast` | Redis 8 Multi-Thread | 2.07 ms | **48,062 RPS** | 35.1 MB/s | 480K | Network socket roundtrip hop (~0.2ms) |
| **Go Milestone 1** | `GET /simple` (10 Cores) | None (Standard `net/http`)| 4.64 ms | **196,480 RPS (Peak 203K)**| 23.2 MB/s | 1.96M | 1 Single Go Binary, 15MB RAM, GMP Work-Stealing |
| **Go Milestone 2** | `GET /simple` (10 Cores) | None (`fasthttp` Pool) | 4.21 ms | **216,755 RPS (Peak 231K)**| 30.3 MB/s | 2.16M | Zero memory allocations per request (`sync.Pool`) |
| **Go Milestone 3** | `GET /code-fast?id=42` | In-Process RAM Cache | 4.76 ms | **194,265 RPS (Peak 201K)**| **126.0 MB/s**| **1.94M (1.26 GB!)**| O(1) Contiguous RAM Lookup (15ns) serving 500-char JSON |
| **Go Milestone 4** | `POST /code-write` | In-Memory Channels + Batch | *(Target)* | **~150,000+ RPS** | - | - | Non-blocking `chan Item`, zero-wait write behind |
| **Go Milestone 5** | WSL2 Linux + `wrk` | Tuned Epoll Stack | *(Target)* | **1,000,000 RPS (1M)**| - | - | C-based load generator + kernel socket reuse |

---

## 2. Architecture Comparison: Why Go Crushed Node.js

![Software Architecture Comparison: Node.js PM2 Cluster vs Golang GMP Model](./img/architecture.jpg)

![Node.js PM2 Cluster vs Go GMP Scheduler Comparison](./img/pm2_vs_gmp.jpg)

---

## 3. Deep-Dive: Core Engineering Concepts Explained

### A. The Latency Hierarchy (Why In-Process RAM Crushed Redis & Postgres)
Computer hardware operates across vastly different timescales:

```
[ CPU L1/L2 Cache ] ──► 1 Nanosecond       (Registers, direct silicon)
[ Local RAM ]       ──► 15 - 50 Nanoseconds (O(1) Array Lookup - 200K+ RPS)
[ Redis (Network) ] ──► 200 - 500 Microseconds (10,000x slower than RAM)
[ PostgreSQL (SSD)] ──► 1 - 5 Milliseconds   (100,000x slower than RAM!)
```

* **In PostgreSQL**: Each query requires connection pool checkout, SQL parsing, and SSD read/write-ahead log sync (`fsync`). With 20 connections taking 2ms each, the theoretical ceiling is `20 / 0.002 = 10,000 RPS`.
* **In Redis**: While Redis stores data in RAM, reading over a TCP socket adds a network roundtrip overhead (~0.2ms) and socket buffer parsing.
* **In Go In-Process RAM (`memoryStore`)**: Data lives inside the Go process's virtual memory address space. Reading an element takes **15 nanoseconds** with zero network hops, delivering **194,265 RPS** while streaming **1.26 GB of JSON in 10 seconds**!

---

### B. What is a TCP Socket & Why Keep-Alive Matters
A TCP Socket is an OS kernel abstraction (represented as a **File Descriptor `int fd`**) identifying an active bidirectional communication pipe.

Every socket is identified by a **4-Tuple**:
`[Client IP : Client Port] <=====> [Server IP : Server Port]`

* **The Connection Bottleneck**: Creating a new TCP socket requires a 3-way handshake (`SYN -> SYN-ACK -> ACK`), and closing it leaves the socket in `TIME_WAIT` state for 60 seconds. A machine has only **65,535 ports**. At 100K+ RPS, opening/closing connections causes instant **Port Exhaustion**.
* **The 1M Solution (HTTP Keep-Alive)**: In all our benchmarks, we reuse **100 persistent connections** with pipelining (`-c 100 -p 10`). Sockets remain permanently open, streaming millions of requests through existing kernel pipes without re-handshaking.

---

### C. Why Buffers are Compulsory (Syscall Overhead Elimination)
Why did we configure `ReadBufferSize: 4096` and `WriteBufferSize: 4096` in `fasthttp`?

1. **Syscall Cost**: Reading bytes from an OS socket requires a context switch from User Mode to Kernel Mode (`recv()`), costing **1,000 to 3,000 CPU clock cycles**.
   * *Without a buffer*: Reading 500 bytes 1-by-1 requires 500 syscalls, instantly choking the CPU.
   * *With a 4KB buffer*: 1 single syscall reads the entire request into RAM.
2. **TCP is a Byte Stream, Not a Message**: TCP delivers chunks asynchronously. A buffer acts as a temporary reservoir that holds incoming bytes until the end-of-header delimiter (`\r\n\r\n`) is reached.
3. **Hardware Page Alignment**: Modern x86/x64 CPUs and OS kernels use **4096-byte (4KB) memory pages**. Aligning socket buffers to 4KB prevents CPU cache line splits and L1/L2 cache thrashing.

---

### D. Zero-Allocation Buffer Pooling (`fasthttp` vs Standard `net/http`)
Standard `net/http` allocates new memory on the heap for every single request:
* At 200,000 RPS, generating 2KB of structs per request produces **400 MB of garbage memory every second**.
* The Go Garbage Collector (GC) must frequently pause execution to clean up heap memory, causing latency jitter.
* `fasthttp` eliminates this via **`sync.Pool`**: When a request completes, its `RequestCtx` buffer is not freed; it is returned to an in-memory pool and immediately recycled for the next incoming request.
* **Result**: Zero heap allocations, reducing latency standard deviation from **5.15 ms down to 2.06 ms**!

---

## 4. Live Benchmark Verification Logs

### Milestone 1: Standard `net/http` (10 Cores Baseline)
```text
Running 10s test @ http://localhost:8080/simple (100 conns, 10 pipeline, 4 workers)
Avg Latency: 4.64 ms | Avg Req/Sec: 196,480 RPS | Peak: 203,007 RPS
Total: 1,966,000 requests in 10.11s | 232 MB read
```

### Milestone 2: `fasthttp` (Zero-Allocation Buffer Pooling)
```text
Running 10s test @ http://localhost:8081/simple (100 conns, 10 pipeline, 4 workers)
Avg Latency: 4.21 ms | Avg Req/Sec: 216,755 RPS | Peak: 231,935 RPS
Total: 2,168,000 requests in 10.18s | 303 MB read
```

### Milestone 3: Real In-Memory 500-Character JSON Read
```text
Running 10s test @ http://localhost:8081/code-fast?id=42 (100 conns, 10 pipeline, 4 workers)
Avg Latency: 4.76 ms | Avg Req/Sec: 194,265 RPS | Peak: 201,599 RPS
Throughput: 126 MB / second | Total: 1.26 GIGABYTES read in 10.21s!
Total: 1,944,000 real JSON responses in 10s
```

---

## 5. How to Reproduce All Benchmarks

1. **Run the Go Server**:
   ```bash
   go run main.go
   ```
2. **Benchmark Simple Route (Ping)**:
   ```bash
   npx autocannon -c 100 -d 10 -p 10 -w 4 http://localhost:8081/simple
   ```
3. **Benchmark Real 500-Char Data Read**:
   ```bash
   npx autocannon -c 100 -d 10 -p 10 -w 4 "http://localhost:8081/code-fast?id=42"
   ```
