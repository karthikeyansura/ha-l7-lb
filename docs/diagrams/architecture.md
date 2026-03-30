# HA-L7-LB Architecture Diagrams

## 1. System Architecture

```mermaid
graph TB
    subgraph Public
        Client[Client]
    end

    subgraph AWS VPC
        subgraph Public Subnet
            NLB["NLB<br/>(L4 TCP)"]
        end

        subgraph Private Subnet - LB Tier
            LB1["LB ECS Task #1<br/>(L7 HTTP Reverse Proxy)"]
            LB2["LB ECS Task #2<br/>(L7 HTTP Reverse Proxy)"]
            LBN["LB ECS Task #N<br/>(Exp 3: scale to 1/2/4/8)"]
        end

        subgraph Private Subnet - Data
            Redis["ElastiCache Redis<br/>(Pub/Sub State Sync)"]
            CloudMap["Cloud Map DNS<br/>(api.internal)"]
        end

        subgraph Private Subnet - Backend Tier
            BE1["Backend ECS Task #1"]
            BE2["Backend ECS Task #2"]
            BEN["Backend ECS Task #N"]
        end
    end

    Client -->|TCP| NLB
    NLB -->|TCP| LB1
    NLB -->|TCP| LB2
    NLB -->|TCP| LBN

    LB1 -->|HTTP| BE1
    LB1 -->|HTTP| BE2
    LB1 -->|HTTP| BEN
    LB2 -->|HTTP| BE1
    LB2 -->|HTTP| BE2
    LB2 -->|HTTP| BEN

    LB1 <-->|Pub/Sub| Redis
    LB2 <-->|Pub/Sub| Redis
    LBN <-->|Pub/Sub| Redis

    LB1 -.->|DNS poll<br/>every 5s| CloudMap
    LB2 -.->|DNS poll<br/>every 5s| CloudMap
    LBN -.->|DNS poll<br/>every 5s| CloudMap

    CloudMap -.->|A records| BE1
    CloudMap -.->|A records| BE2
    CloudMap -.->|A records| BEN

    style NLB fill:#f9a825,stroke:#f57f17,color:#000
    style Redis fill:#d32f2f,stroke:#b71c1c,color:#fff
    style CloudMap fill:#1565c0,stroke:#0d47a1,color:#fff
    style LB1 fill:#2e7d32,stroke:#1b5e20,color:#fff
    style LB2 fill:#2e7d32,stroke:#1b5e20,color:#fff
    style LBN fill:#2e7d32,stroke:#1b5e20,color:#fff
    style BE1 fill:#6a1b9a,stroke:#4a148c,color:#fff
    style BE2 fill:#6a1b9a,stroke:#4a148c,color:#fff
    style BEN fill:#6a1b9a,stroke:#4a148c,color:#fff
```

**Experiment configurations:**
- **Sai's runs (homogeneous):** identical backends, same CPU/memory -- establishes baseline algorithm comparison
- **Joshua's runs (heterogeneous):** 1 strong (512 CPU / 1024 MB) + 1 weak (256 CPU / 512 MB) -- tests algorithm sensitivity to uneven backend capacity
- **Experiment 3:** LB count scales to 1/2/4/8 behind NLB

## 2. Request Lifecycle (proxy.ServeHTTP)

```mermaid
flowchart TD
    Start([Incoming HTTP Request]) --> MaxBytes["http.MaxBytesReader<br/>(10MB body limit)"]
    MaxBytes --> Buffer["Buffer entire request body<br/>(io.ReadAll for retry replay)"]
    Buffer --> BufferErr{Body read<br/>error?}
    BufferErr -->|"Yes (oversized)"| Err413[Return 413 Request Entity Too Large]
    BufferErr -->|"Yes (other)"| Err500[Return 500 Internal Server Error]
    BufferErr -->|No| CheckHealthy["pool.GetHealthy()"]

    CheckHealthy --> HasBackends{Healthy<br/>backends > 0?}
    HasBackends -->|No| Err503[Return 503 No Healthy Backends]
    HasBackends -->|Yes| SelectBackend["algo.GetTarget()<br/>(RoundRobin / LeastConn / Weighted)"]

    SelectBackend --> SelectErr{Algorithm<br/>error?}
    SelectErr -->|Yes| Err503b[Return 503 Service Unavailable]
    SelectErr -->|No| AddConn["pool.AddConnections(backend, 1)"]

    AddConn --> ResetBody1["resetBody(req)<br/>Restore Body + ContentLength"]
    ResetBody1 --> Proxy1["proxyRequest(backend)<br/>configurable timeout from config.yaml"]

    Proxy1 --> RemoveConn1["pool.RemoveConnections(backend, 1)"]
    RemoveConn1 --> Success1{Request<br/>succeeded?}

    Success1 -->|Yes| RecordOK["collector.RecordRequest<br/>(success=true, retried=false)"]
    RecordOK --> ReturnOK([Return Response to Client])

    Success1 -->|No| CheckDisconnect{Client<br/>disconnected?}
    CheckDisconnect -->|"Yes (context.Canceled,<br/>broken pipe, reset)"| RecordDisconnect["collector.RecordRequest<br/>(success=false, timeout=false)"]
    RecordDisconnect --> SilentReturn([Return silently<br/>client already gone])

    CheckDisconnect -->|No| CheckMethod{Idempotent?<br/>GET / PUT / DELETE}

    CheckMethod -->|"No (POST/PATCH)"| RecordFail["collector.RecordRequest<br/>(success=false, timeout=true)"]
    RecordFail --> Err504[Return 504 Gateway Timeout]

    CheckMethod -->|Yes| CheckBudget{"Retry budget OK?<br/>activeRetries / activeRequests<br/>≤ 20%"}
    CheckBudget -->|No| RecordFail

    CheckBudget -->|Yes| MarkDown["pool.MarkHealthy(backend, false)<br/>(immediate local update)"]
    MarkDown --> AsyncRedis["go updater.UpdateBackendStatus(DOWN)<br/>(async fire-and-forget, debounced)"]
    AsyncRedis --> FreshList["pool.GetHealthy()<br/>(fresh snapshot excludes downed backend)"]
    FreshList --> SelectDiff["selectDifferent()<br/>Pick candidate with min active connections"]

    SelectDiff --> HasRetry{Different<br/>backend found?}
    HasRetry -->|No| RecordFail
    HasRetry -->|Yes| AddConn2["pool.AddConnections(retryBackend, 1)"]

    AddConn2 --> ResetBody2["resetBody(req)<br/>Replay buffered body"]
    ResetBody2 --> Proxy2["proxyRequest(retryBackend)<br/>configurable timeout from config.yaml"]
    Proxy2 --> Success2{Retry<br/>succeeded?}

    Success2 -->|Yes| RemoveConn2["pool.RemoveConnections(retryBackend, 1)"]
    RemoveConn2 --> RecordRetry["collector.RecordRequest<br/>(success=true, retried=true)"]
    RecordRetry --> ReturnOK

    Success2 -->|No| RemoveConn2b["pool.RemoveConnections(retryBackend, 1)"]
    RemoveConn2b --> RecordFail

    style Start fill:#1565c0,stroke:#0d47a1,color:#fff
    style ReturnOK fill:#2e7d32,stroke:#1b5e20,color:#fff
    style Err413 fill:#d32f2f,stroke:#b71c1c,color:#fff
    style Err500 fill:#d32f2f,stroke:#b71c1c,color:#fff
    style Err503 fill:#d32f2f,stroke:#b71c1c,color:#fff
    style Err503b fill:#d32f2f,stroke:#b71c1c,color:#fff
    style Err504 fill:#d32f2f,stroke:#b71c1c,color:#fff
    style AsyncRedis fill:#f9a825,stroke:#f57f17,color:#000
    style CheckBudget fill:#ff8f00,stroke:#e65100,color:#000
    style CheckDisconnect fill:#ff8f00,stroke:#e65100,color:#000
    style SilentReturn fill:#78909c,stroke:#546e7a,color:#fff
    style RecordDisconnect fill:#78909c,stroke:#546e7a,color:#fff
```

## 3. Health State Propagation

```mermaid
sequenceDiagram
    participant BE as Backend #2
    participant HC1 as LB #1<br/>Health Checker
    participant Pool1 as LB #1<br/>InMemory Pool
    participant Redis as ElastiCache Redis
    participant Pool2 as LB #2<br/>InMemory Pool
    participant Pool3 as LB #3<br/>InMemory Pool

    Note over HC1: Periodic health check cycle (configurable interval)

    rect rgb(255, 235, 238)
        Note right of BE: Backend failure detected
        HC1->>BE: GET /health
        BE--xHC1: Timeout / Connection Refused / Non-200

        HC1->>Pool1: MarkHealthy(backend#2, false)
        Note over Pool1: Immediate local update<br/>atomic.Bool.Store(false)

        HC1->>Redis: SET backend:http://backend-2:8080 = "DOWN"
        HC1->>Redis: PUBLISH "lb-backend-events"<br/>"http://backend-2:8080|DOWN"

        Redis-->>Pool2: Message: "http://backend-2:8080|DOWN"
        Pool2->>Pool2: MarkHealthy(backend#2, false)

        Redis-->>Pool3: Message: "http://backend-2:8080|DOWN"
        Pool3->>Pool3: MarkHealthy(backend#2, false)
    end

    Note over HC1: Subsequent health check cycles...

    rect rgb(232, 245, 233)
        Note right of BE: Backend recovery detected
        HC1->>BE: GET /health
        BE-->>HC1: 200 OK

        HC1->>Pool1: MarkHealthy(backend#2, true)
        Note over Pool1: Immediate local update<br/>atomic.Bool.Store(true)

        HC1->>Redis: SET backend:http://backend-2:8080 = "UP"
        HC1->>Redis: PUBLISH "lb-backend-events"<br/>"http://backend-2:8080|UP"

        Redis-->>Pool2: Message: "http://backend-2:8080|UP"
        Pool2->>Pool2: MarkHealthy(backend#2, true)

        Redis-->>Pool3: Message: "http://backend-2:8080|UP"
        Pool3->>Pool3: MarkHealthy(backend#2, true)
    end
```

## 4. Package Dependency Map

```mermaid
graph TD
    Main["cmd/lb/main.go"]

    Config["internal/config"]
    Algorithms["internal/algorithms"]
    Discovery["internal/discovery"]
    Health["internal/health<br/>(Checker + StatusUpdater iface)"]
    Metrics["internal/metrics"]
    Proxy["internal/proxy"]
    Repository["internal/repository"]
    RedisMgr["internal/repository/<br/>redismanager"]

    Main --> Config
    Main --> Algorithms
    Main --> Discovery
    Main --> Health
    Main --> Metrics
    Main --> Proxy
    Main --> Repository
    Main --> RedisMgr

    Proxy --> Repository
    Proxy --> Algorithms
    Proxy --> Metrics
    Proxy -->|"StatusUpdater iface"| Health

    Health --> Repository

    Discovery --> Repository

    RedisMgr --> Repository

    Algorithms --> Repository

    style Main fill:#1565c0,stroke:#0d47a1,color:#fff
    style Proxy fill:#2e7d32,stroke:#1b5e20,color:#fff
    style Repository fill:#6a1b9a,stroke:#4a148c,color:#fff
    style RedisMgr fill:#d32f2f,stroke:#b71c1c,color:#fff
    style Metrics fill:#f9a825,stroke:#f57f17,color:#000
    style Health fill:#00838f,stroke:#006064,color:#fff
    style Algorithms fill:#e65100,stroke:#bf360c,color:#fff
    style Discovery fill:#4527a0,stroke:#311b92,color:#fff
    style Config fill:#78909c,stroke:#546e7a,color:#fff
```

## 5. Algorithm Comparison

```mermaid
graph LR
    subgraph RoundRobin["Round Robin (Stateless)"]
        direction TB
        RR_In([Request]) --> RR_Atomic["atomic.AddUint64(&next, 1)"]
        RR_Atomic --> RR_Mod["index = (next - 1) % len(healthy)"]
        RR_Mod --> RR_Pick["Return healthy[index]"]

        RR_Note["No per-backend state<br/>Lock-free via atomic counter<br/>Uniform distribution"]

        style RR_Note fill:#fff,stroke:#999,color:#555
    end

    subgraph LeastConn["Least Connections (Power of Two Choices)"]
        direction TB
        LC_In([Request]) --> LC_Pick2["Pick 2 random backends<br/>rand.Intn(len(healthy))"]
        LC_Pick2 --> LC_Compare["Compare ActiveConnections<br/>(atomic.LoadInt64 reads)"]
        LC_Compare --> LC_Return["Return backend with<br/>fewer active connections"]

        LC_Note["O(1) selection, not O(n) scan<br/>Avoids herd effect on global min<br/>Better for multi-LB with local counters"]

        style LC_Note fill:#fff,stroke:#999,color:#555
    end

    subgraph Weighted["Weighted (Proportional)"]
        direction TB
        W_In([Request]) --> W_Candidates["Build candidate list<br/>from healthy backends"]
        W_Candidates --> W_Init["Lazy init: weight not seen?<br/>Set [original, remaining]"]
        W_Init --> W_Pick["rand.Intn(len(candidates))"]
        W_Pick --> W_Check{remaining<br/>> 0?}
        W_Check -->|Yes| W_Dec["Decrement remaining weight<br/>Return backend"]
        W_Check -->|No| W_Remove["Remove from candidates<br/>Try next random pick"]
        W_Remove --> W_Empty{All<br/>depleted?}
        W_Empty -->|No| W_Pick
        W_Empty -->|Yes| W_Reset["Reset all counters<br/>to original weights<br/>Return last candidate"]

        W_Note["sync.RWMutex (write-locked)<br/>Epoch-based reset<br/>Proportional distribution"]

        style W_Note fill:#fff,stroke:#999,color:#555
    end

    style RoundRobin fill:#e3f2fd,stroke:#1565c0,color:#000
    style LeastConn fill:#e8f5e9,stroke:#2e7d32,color:#000
    style Weighted fill:#fff3e0,stroke:#e65100,color:#000
```
