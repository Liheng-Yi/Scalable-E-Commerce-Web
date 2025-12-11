# Scalable E-Commerce Web System

> **A distributed e-commerce platform built to explore scalability, fault tolerance, and caching strategies under real-world conditions.**

---

## Why I Built This

Modern e-commerce platforms face intense challenges: flash sales that spike traffic 100x, database failures that can't disrupt checkout, and latency requirements measured in milliseconds. I wanted to go beyond textbook definitions and **actually build and break** a system to understand how scalability and resilience work in practice.

This project is a hands-on exploration of:
- **Horizontal Scaling** — How does an API layer behave under increasing load? At what point does it buckle?
- **Caching Strategies** — Redis vs. DynamoDB: how much latency can we shave off for hot products?
- **Failure Recovery** — Circuit breakers, retry patterns with exponential backoff — do they actually save you when things go wrong?
- **Performance Benchmarking** — Real numbers from Locust load tests, not just theory.

---

## Architecture Overview

```
                                    ┌─────────────────┐
                                    │   CloudWatch    │
                                    │   Monitoring    │
                                    └────────┬────────┘
                                             │
┌──────────┐     ┌─────────────┐     ┌───────┴───────┐     ┌─────────────┐
│  Locust  │────▶│    ALB      │────▶│   ECS Fargate │────▶│  DynamoDB   │
│  (Load)  │     │ (Load Bal.) │     │   (Go API)    │     │  (Storage)  │
└──────────┘     └─────────────┘     └───────┬───────┘     └─────────────┘
                                             │
                                     ┌───────┴───────┐
                                     │  ElastiCache  │
                                     │   (Redis)     │
                                     └───────────────┘
```

**Tech Stack:**
- **API Service:** Go with Gorilla Mux — handles product browsing, cart, and checkout
- **Database:** AWS DynamoDB — stores products, carts, and orders
- **Cache:** AWS ElastiCache (Redis) — caches hot products for sub-millisecond reads
- **Infrastructure:** Terraform + ECS Fargate — fully containerized, auto-scaling deployment
- **Load Testing:** Locust — simulates realistic user traffic patterns

---

## Scalability Experiments

| Experiment | Goal | How |
|------------|------|-----|
| **Load Test** | Measure throughput & latency under stress | Locust simulating 100→1000+ concurrent users |
| **Cache Comparison** | Quantify Redis speedup | Toggle caching on/off, compare p50/p95 latencies |
| **Failure Recovery** | Validate resilience patterns | Inject failures, observe circuit breaker behavior |
| **Hot Product Test** | Optimize for popular items | Pre-warm Redis with trending products, benchmark |

---

## Features

- **GET /products/{productId}** — Retrieve product details by ID
- **POST /products/{productId}/details** — Create or update product information
- **Shopping Cart API** — Add items, update quantities, checkout flow
- **Order Processing** — Asynchronous order fulfillment
- **Resilience Patterns** — Circuit breakers, retry with exponential backoff
- **Real-time Monitoring** — CloudWatch metrics and logging
- **Docker + Terraform** — One-command deployment to AWS

---

## Prerequisites

- Go 1.21 or higher
- Docker (optional, for containerization)

## Installation & Running

### Local Development

1. Clone the repository:
```bash
git clone https://github.com/Liheng-Yi/Scalable-E-Commerce-Web.git
cd Scalable-E-Commerce-Web
```

2. Download dependencies:
```bash
go mod download
```

3. Run the server:
```bash
go run main.go
```

The server will start on `http://localhost:8080`

### Using Docker

1. Build the Docker image:
```bash
docker build -t product-api .
```

2. Run the container:
```bash
docker run -p 8080:8080 product-api
```

## Automated Testing

### Using the Test Script

**Option 1: Simple test script (no dependencies)**
```bash
chmod +x test_api_simple.sh
./test_api_simple.sh
```

**Option 2: Test script with jq for formatted output**

If you have `jq` installed:
```bash
chmod +x test_api.sh
./test_api.sh
```

## Implementation Details

- **Thread-Safe Storage**: Uses `sync.RWMutex` for concurrent access to the in-memory product store
- **Input Validation**: All fields are validated according to OpenAPI spec constraints
- **Error Handling**: Comprehensive error responses with meaningful messages
- **RESTful Design**: Follows REST principles and HTTP standards
- **Gorilla Mux Router**: Uses the popular Gorilla Mux for flexible routing

## Failure Recovery - Circuit Breaker & Retry Patterns

The API implements enterprise-grade failure recovery patterns to handle database and payment service failures gracefully.

### Circuit Breaker Pattern

Prevents cascading failures by temporarily stopping requests to failing services.

```
Normal Flow:    Request → Service → Response
                    ↓
After Failures: Request → Circuit OPEN → Immediate Rejection (503)
                    ↓ (after timeout)
Recovery Test:  Request → Circuit HALF-OPEN → Test Request
                    ↓ (success)
Recovered:      Request → Circuit CLOSED → Normal Flow
```
