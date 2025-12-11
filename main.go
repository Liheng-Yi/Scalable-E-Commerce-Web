package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"math/rand"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/gorilla/mux"
	"github.com/redis/go-redis/v9"
)

// ============================================================================
// CIRCUIT BREAKER PATTERN
// ============================================================================

// CircuitState represents the state of a circuit breaker
type CircuitState int

const (
	CircuitClosed CircuitState = iota // Normal operation
	CircuitOpen                       // Failing, reject requests
	CircuitHalfOpen                   // Testing if service recovered
)

func (s CircuitState) String() string {
	switch s {
	case CircuitClosed:
		return "CLOSED"
	case CircuitOpen:
		return "OPEN"
	case CircuitHalfOpen:
		return "HALF_OPEN"
	default:
		return "UNKNOWN"
	}
}

// CircuitBreaker implements the circuit breaker pattern
type CircuitBreaker struct {
	name            string
	state           CircuitState
	failures        int64
	successes       int64
	lastFailure     time.Time
	failureThreshold int64         // Number of failures before opening
	successThreshold int64         // Successes needed to close from half-open
	timeout          time.Duration // Time to wait before half-open
	mu               sync.RWMutex
	
	// Stats for monitoring
	totalRequests    int64
	totalFailures    int64
	totalSuccesses   int64
	totalRejected    int64
	stateChanges     int64
}

// NewCircuitBreaker creates a new circuit breaker
func NewCircuitBreaker(name string, failureThreshold, successThreshold int64, timeout time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		name:             name,
		state:            CircuitClosed,
		failureThreshold: failureThreshold,
		successThreshold: successThreshold,
		timeout:          timeout,
	}
}

// ErrCircuitOpen is returned when the circuit is open
var ErrCircuitOpen = errors.New("circuit breaker is open")

// Execute runs the given function with circuit breaker protection
func (cb *CircuitBreaker) Execute(fn func() error) error {
	atomic.AddInt64(&cb.totalRequests, 1)
	
	if !cb.allowRequest() {
		atomic.AddInt64(&cb.totalRejected, 1)
		return ErrCircuitOpen
	}
	
	err := fn()
	cb.recordResult(err)
	return err
}

// allowRequest checks if a request should be allowed
func (cb *CircuitBreaker) allowRequest() bool {
	cb.mu.RLock()
	state := cb.state
	lastFailure := cb.lastFailure
	cb.mu.RUnlock()
	
	switch state {
	case CircuitClosed:
		return true
	case CircuitOpen:
		// Check if timeout has passed to transition to half-open
		if time.Since(lastFailure) > cb.timeout {
			cb.mu.Lock()
			if cb.state == CircuitOpen {
				cb.state = CircuitHalfOpen
				cb.successes = 0
				atomic.AddInt64(&cb.stateChanges, 1)
				log.Printf("🔄 Circuit Breaker [%s]: OPEN → HALF_OPEN (testing recovery)", cb.name)
			}
			cb.mu.Unlock()
			return true
		}
		return false
	case CircuitHalfOpen:
		return true
	default:
		return true
	}
}

// recordResult records the result of a request
func (cb *CircuitBreaker) recordResult(err error) {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	
	if err != nil {
		cb.failures++
		cb.lastFailure = time.Now()
		atomic.AddInt64(&cb.totalFailures, 1)
		
		switch cb.state {
		case CircuitClosed:
			if cb.failures >= cb.failureThreshold {
				cb.state = CircuitOpen
				atomic.AddInt64(&cb.stateChanges, 1)
				log.Printf("🔴 Circuit Breaker [%s]: CLOSED → OPEN (failures: %d)", cb.name, cb.failures)
			}
		case CircuitHalfOpen:
			cb.state = CircuitOpen
			cb.failures = 0
			atomic.AddInt64(&cb.stateChanges, 1)
			log.Printf("🔴 Circuit Breaker [%s]: HALF_OPEN → OPEN (failure during recovery)", cb.name)
		}
	} else {
		cb.successes++
		atomic.AddInt64(&cb.totalSuccesses, 1)
		
		switch cb.state {
		case CircuitClosed:
			cb.failures = 0 // Reset failures on success
		case CircuitHalfOpen:
			if cb.successes >= cb.successThreshold {
				cb.state = CircuitClosed
				cb.failures = 0
				cb.successes = 0
				atomic.AddInt64(&cb.stateChanges, 1)
				log.Printf("🟢 Circuit Breaker [%s]: HALF_OPEN → CLOSED (service recovered)", cb.name)
			}
		}
	}
}

// GetState returns the current state
func (cb *CircuitBreaker) GetState() CircuitState {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}

// GetStats returns circuit breaker statistics
func (cb *CircuitBreaker) GetStats() map[string]interface{} {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	
	return map[string]interface{}{
		"name":              cb.name,
		"state":             cb.state.String(),
		"current_failures":  cb.failures,
		"current_successes": cb.successes,
		"failure_threshold": cb.failureThreshold,
		"success_threshold": cb.successThreshold,
		"timeout_seconds":   cb.timeout.Seconds(),
		"total_requests":    atomic.LoadInt64(&cb.totalRequests),
		"total_failures":    atomic.LoadInt64(&cb.totalFailures),
		"total_successes":   atomic.LoadInt64(&cb.totalSuccesses),
		"total_rejected":    atomic.LoadInt64(&cb.totalRejected),
		"state_changes":     atomic.LoadInt64(&cb.stateChanges),
	}
}

// Reset resets the circuit breaker to closed state
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.state = CircuitClosed
	cb.failures = 0
	cb.successes = 0
	log.Printf("🔄 Circuit Breaker [%s]: RESET to CLOSED", cb.name)
}

// ============================================================================
// RETRY PATTERN WITH EXPONENTIAL BACKOFF
// ============================================================================

// RetryConfig configures retry behavior
type RetryConfig struct {
	MaxRetries      int           // Maximum number of retry attempts
	InitialDelay    time.Duration // Initial delay before first retry
	MaxDelay        time.Duration // Maximum delay between retries
	BackoffFactor   float64       // Multiplier for exponential backoff
	Jitter          bool          // Add random jitter to delays
	RetryableErrors []error       // Specific errors to retry (nil = all errors)
}

// DefaultRetryConfig returns a sensible default retry configuration
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries:    3,
		InitialDelay:  100 * time.Millisecond,
		MaxDelay:      5 * time.Second,
		BackoffFactor: 2.0,
		Jitter:        true,
	}
}

// RetryResult contains information about the retry operation
type RetryResult struct {
	Success      bool          `json:"success"`
	Attempts     int           `json:"attempts"`
	TotalTime    time.Duration `json:"total_time"`
	FinalError   error         `json:"-"`
	ErrorMessage string        `json:"error,omitempty"`
}

// RetryWithBackoff executes a function with exponential backoff retry
func RetryWithBackoff(ctx context.Context, config RetryConfig, operation string, fn func() error) RetryResult {
	startTime := time.Now()
	var lastErr error
	
	for attempt := 0; attempt <= config.MaxRetries; attempt++ {
		if attempt > 0 {
			delay := calculateDelay(config, attempt)
			log.Printf("🔄 Retry [%s]: Attempt %d/%d after %v delay", operation, attempt, config.MaxRetries, delay)
			
			select {
			case <-ctx.Done():
				return RetryResult{
					Success:      false,
					Attempts:     attempt,
					TotalTime:    time.Since(startTime),
					FinalError:   ctx.Err(),
					ErrorMessage: "context cancelled",
				}
			case <-time.After(delay):
			}
		}
		
		err := fn()
		if err == nil {
			if attempt > 0 {
				log.Printf("✅ Retry [%s]: Succeeded on attempt %d", operation, attempt+1)
			}
			return RetryResult{
				Success:   true,
				Attempts:  attempt + 1,
				TotalTime: time.Since(startTime),
			}
		}
		
		lastErr = err
		log.Printf("⚠️ Retry [%s]: Attempt %d failed: %v", operation, attempt+1, err)
		
		// Check if error is retryable
		if !isRetryable(config, err) {
			log.Printf("❌ Retry [%s]: Error is not retryable", operation)
			break
		}
	}
	
	log.Printf("❌ Retry [%s]: All %d attempts failed", operation, config.MaxRetries+1)
	return RetryResult{
		Success:      false,
		Attempts:     config.MaxRetries + 1,
		TotalTime:    time.Since(startTime),
		FinalError:   lastErr,
		ErrorMessage: lastErr.Error(),
	}
}

// calculateDelay calculates the delay for the given attempt
func calculateDelay(config RetryConfig, attempt int) time.Duration {
	delay := float64(config.InitialDelay) * math.Pow(config.BackoffFactor, float64(attempt-1))
	
	if config.Jitter {
		// Add up to 25% jitter
		jitterFactor := 0.75 + rand.Float64()*0.5
		delay *= jitterFactor
	}
	
	if delay > float64(config.MaxDelay) {
		delay = float64(config.MaxDelay)
	}
	
	return time.Duration(delay)
}

// isRetryable checks if an error should be retried
func isRetryable(config RetryConfig, err error) bool {
	if len(config.RetryableErrors) == 0 {
		return true // Retry all errors if none specified
	}
	
	for _, retryableErr := range config.RetryableErrors {
		if errors.Is(err, retryableErr) {
			return true
		}
	}
	return false
}

// ============================================================================
// SIMULATED PAYMENT SERVICE
// ============================================================================

// PaymentService simulates an external payment processor
type PaymentService struct {
	failureRate     float64 // 0.0 to 1.0 - probability of failure
	latencyMs       int     // Simulated latency in milliseconds
	circuitBreaker  *CircuitBreaker
	mu              sync.RWMutex
	
	// Stats
	totalPayments     int64
	successfulPayments int64
	failedPayments    int64
}

// PaymentRequest represents a payment request
type PaymentRequest struct {
	OrderID     string  `json:"order_id"`
	Amount      float64 `json:"amount"`
	UserID      string  `json:"user_id"`
	CardNumber  string  `json:"card_number"` // Simulated - last 4 digits only
}

// PaymentResponse represents a payment response
type PaymentResponse struct {
	TransactionID string         `json:"transaction_id"`
	Status        string         `json:"status"`
	Message       string         `json:"message,omitempty"`
	Amount        float64        `json:"amount"`
	ProcessedAt   string         `json:"processed_at"`
	RetryInfo     *RetryResult   `json:"retry_info,omitempty"`
}

// PaymentError represents a payment processing error
type PaymentError struct {
	Code    string
	Message string
}

func (e *PaymentError) Error() string {
	return fmt.Sprintf("payment error [%s]: %s", e.Code, e.Message)
}

var (
	ErrPaymentDeclined    = &PaymentError{Code: "DECLINED", Message: "Payment was declined"}
	ErrPaymentTimeout     = &PaymentError{Code: "TIMEOUT", Message: "Payment processing timeout"}
	ErrPaymentUnavailable = &PaymentError{Code: "UNAVAILABLE", Message: "Payment service unavailable"}
)

// NewPaymentService creates a new payment service
func NewPaymentService() *PaymentService {
	return &PaymentService{
		failureRate:    0.0, // No failures by default
		latencyMs:      50,  // 50ms default latency
		circuitBreaker: NewCircuitBreaker("payment", 3, 2, 10*time.Second),
	}
}

// ProcessPayment processes a payment with retry and circuit breaker
func (ps *PaymentService) ProcessPayment(ctx context.Context, req PaymentRequest) (*PaymentResponse, error) {
	atomic.AddInt64(&ps.totalPayments, 1)
	
	retryConfig := RetryConfig{
		MaxRetries:    3,
		InitialDelay:  200 * time.Millisecond,
		MaxDelay:      2 * time.Second,
		BackoffFactor: 2.0,
		Jitter:        true,
	}
	
	var response *PaymentResponse
	
	result := RetryWithBackoff(ctx, retryConfig, "payment", func() error {
		return ps.circuitBreaker.Execute(func() error {
			resp, err := ps.doProcessPayment(req)
			if err != nil {
				return err
			}
			response = resp
			return nil
		})
	})
	
	if !result.Success {
		atomic.AddInt64(&ps.failedPayments, 1)
		return &PaymentResponse{
			Status:      "failed",
			Message:     result.ErrorMessage,
			Amount:      req.Amount,
			ProcessedAt: time.Now().Format(time.RFC3339),
			RetryInfo:   &result,
		}, result.FinalError
	}
	
	atomic.AddInt64(&ps.successfulPayments, 1)
	response.RetryInfo = &result
	return response, nil
}

// doProcessPayment is the actual payment processing logic
func (ps *PaymentService) doProcessPayment(req PaymentRequest) (*PaymentResponse, error) {
	ps.mu.RLock()
	failureRate := ps.failureRate
	latencyMs := ps.latencyMs
	ps.mu.RUnlock()
	
	// Simulate processing latency
	time.Sleep(time.Duration(latencyMs) * time.Millisecond)
	
	// Simulate random failures based on failure rate
	if rand.Float64() < failureRate {
		// Randomly choose failure type
		failureType := rand.Intn(3)
		switch failureType {
		case 0:
			return nil, ErrPaymentDeclined
		case 1:
			return nil, ErrPaymentTimeout
		default:
			return nil, ErrPaymentUnavailable
		}
	}
	
	return &PaymentResponse{
		TransactionID: fmt.Sprintf("TXN-%d", time.Now().UnixNano()),
		Status:        "success",
		Message:       "Payment processed successfully",
		Amount:        req.Amount,
		ProcessedAt:   time.Now().Format(time.RFC3339),
	}, nil
}

// SetFailureRate sets the failure rate for testing
func (ps *PaymentService) SetFailureRate(rate float64) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.failureRate = rate
	log.Printf("💳 Payment Service: Failure rate set to %.1f%%", rate*100)
}

// SetLatency sets the processing latency for testing
func (ps *PaymentService) SetLatency(ms int) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.latencyMs = ms
	log.Printf("💳 Payment Service: Latency set to %dms", ms)
}

// GetStats returns payment service statistics
func (ps *PaymentService) GetStats() map[string]interface{} {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	
	return map[string]interface{}{
		"total_payments":      atomic.LoadInt64(&ps.totalPayments),
		"successful_payments": atomic.LoadInt64(&ps.successfulPayments),
		"failed_payments":     atomic.LoadInt64(&ps.failedPayments),
		"failure_rate":        ps.failureRate,
		"latency_ms":          ps.latencyMs,
		"circuit_breaker":     ps.circuitBreaker.GetStats(),
	}
}

// ResetCircuitBreaker resets the payment circuit breaker
func (ps *PaymentService) ResetCircuitBreaker() {
	ps.circuitBreaker.Reset()
}

// Global circuit breakers and payment service
var (
	dynamoDBCircuitBreaker *CircuitBreaker
	redisCircuitBreaker    *CircuitBreaker
	paymentService         *PaymentService
)

// Failure simulation flags
var (
	simulateDynamoDBFailure bool
	simulateRedisFailure    bool
	failureSimMutex         sync.RWMutex
)

// Product represents a product with searchable fields
type Product struct {
	ID          int     `json:"id" dynamodbav:"id"`
	Name        string  `json:"name" dynamodbav:"name"`
	Category    string  `json:"category" dynamodbav:"category"`
	Description string  `json:"description" dynamodbav:"description"`
	Brand       string  `json:"brand" dynamodbav:"brand"`
	Price       float64 `json:"price" dynamodbav:"price"`
}

// SearchResponse represents the search result structure
type SearchResponse struct {
	Products     []Product `json:"products"`
	TotalFound   int       `json:"total_found"`
	SearchTime   string    `json:"search_time"`
	CheckedCount int       `json:"checked_count"`
}

// Cart represents a shopping cart (stored in DynamoDB)
type Cart struct {
	UserID    string     `json:"user_id" dynamodbav:"user_id"`
	Items     []CartItem `json:"items" dynamodbav:"items"`
	Total     float64    `json:"total" dynamodbav:"total"`
	UpdatedAt string     `json:"updated_at" dynamodbav:"updated_at"`
	TTL       int64      `json:"ttl,omitempty" dynamodbav:"ttl,omitempty"`
}

// CartItem represents an item in the cart
type CartItem struct {
	ProductID int     `json:"product_id" dynamodbav:"product_id"`
	Quantity  int     `json:"quantity" dynamodbav:"quantity"`
	Price     float64 `json:"price" dynamodbav:"price"`
}

// AddToCartRequest represents the request to add item to cart
type AddToCartRequest struct {
	ProductID int `json:"product_id"`
	Quantity  int `json:"quantity"`
}

// Order represents a completed order (stored in DynamoDB)
type Order struct {
	OrderID   string     `json:"order_id" dynamodbav:"order_id"`
	UserID    string     `json:"user_id" dynamodbav:"user_id"`
	Items     []CartItem `json:"items" dynamodbav:"items"`
	Total     float64    `json:"total" dynamodbav:"total"`
	Status    string     `json:"status" dynamodbav:"status"`
	CreatedAt string     `json:"created_at" dynamodbav:"created_at"`
}

// CompareResponse shows latency comparison between Redis and in-memory
type CompareResponse struct {
	ProductID     int     `json:"product_id"`
	Product       Product `json:"product"`
	RedisLatency  string  `json:"redis_latency"`
	MemoryLatency string  `json:"memory_latency"`
	CacheHit      bool    `json:"cache_hit"`
	SpeedupFactor float64 `json:"speedup_factor,omitempty"`
}

// DynamoDB client and table names
var (
	dynamoClient      *dynamodb.Client
	productsTableName string
	cartsTableName    string
	ordersTableName   string
	useDynamoDB       bool
)

// Redis client
var (
	redisClient  *redis.Client
	redisEnabled bool
	redisEndpoint string
)

// In-memory product store for fast search (100k products)
var productStore sync.Map
var productIDs []int

// Fallback in-memory stores when DynamoDB is not available
var cartStore sync.Map
var orderStore sync.Map

// Stats for tracking
var totalRequestsProcessed int64
var totalReadOperations int64
var totalWriteOperations int64
var totalDynamoDBReads int64
var totalDynamoDBWrites int64
var totalRedisHits int64
var totalRedisMisses int64
var totalRedisWrites int64

// Hot product cache key prefix
const hotProductKeyPrefix = "hot:product:"
const hotProductTTL = 5 * time.Minute

// Sample data for variety
var brands = []string{"Alpha", "Beta", "Gamma", "Delta", "Epsilon", "Zeta", "Eta", "Theta"}
var categories = []string{"Electronics", "Books", "Home", "Sports", "Fashion", "Toys", "Garden", "Automotive"}

// initRedis initializes the Redis client
func initRedis() {
	redisEndpoint = os.Getenv("REDIS_ENDPOINT")
	
	if redisEndpoint == "" {
		log.Println("⚠️  Redis endpoint not configured, hot product caching disabled")
		redisEnabled = false
		return
	}
	
	redisClient = redis.NewClient(&redis.Options{
		Addr:         redisEndpoint,
		Password:     "", // No password for ElastiCache
		DB:           0,
		DialTimeout:  5 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
		PoolSize:     10,
	})
	
	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	
	_, err := redisClient.Ping(ctx).Result()
	if err != nil {
		log.Printf("⚠️  Failed to connect to Redis: %v, hot product caching disabled", err)
		redisEnabled = false
		return
	}
	
	redisEnabled = true
	log.Println("✅ Redis client initialized successfully")
	log.Printf("   Redis Endpoint: %s", redisEndpoint)
	
	// Initialize Redis circuit breaker
	redisCircuitBreaker = NewCircuitBreaker("redis", 5, 3, 30*time.Second)
	log.Println("✅ Redis Circuit Breaker initialized")
}

// initDynamoDB initializes the DynamoDB client
func initDynamoDB() {
	productsTableName = os.Getenv("DYNAMODB_PRODUCTS_TABLE")
	cartsTableName = os.Getenv("DYNAMODB_CARTS_TABLE")
	ordersTableName = os.Getenv("DYNAMODB_ORDERS_TABLE")
	
	if cartsTableName == "" || ordersTableName == "" {
		log.Println("⚠️  DynamoDB tables not configured, using in-memory storage")
		useDynamoDB = false
		return
	}
	
	cfg, err := config.LoadDefaultConfig(context.TODO())
	if err != nil {
		log.Printf("⚠️  Failed to load AWS config: %v, using in-memory storage", err)
		useDynamoDB = false
		return
	}
	
	dynamoClient = dynamodb.NewFromConfig(cfg)
	useDynamoDB = true
	log.Println("✅ DynamoDB client initialized successfully")
	log.Printf("   Products Table: %s", productsTableName)
	log.Printf("   Carts Table: %s", cartsTableName)
	log.Printf("   Orders Table: %s", ordersTableName)
	
	// Initialize DynamoDB circuit breaker
	dynamoDBCircuitBreaker = NewCircuitBreaker("dynamodb", 5, 3, 30*time.Second)
	log.Println("✅ DynamoDB Circuit Breaker initialized")
}

// initPaymentService initializes the payment service
func initPaymentService() {
	paymentService = NewPaymentService()
	log.Println("✅ Payment Service initialized with circuit breaker")
}

// initializeProducts generates 100,000 products at startup
func initializeProducts() {
	log.Println("Generating 100,000 products...")
	start := time.Now()

	productIDs = make([]int, 100000)
	
	for i := 1; i <= 100000; i++ {
		brand := brands[i%len(brands)]
		category := categories[i%len(categories)]
		
		product := Product{
			ID:          i,
			Name:        fmt.Sprintf("Product %s %d", brand, i),
			Category:    category,
			Description: fmt.Sprintf("High-quality %s product from %s brand", category, brand),
			Brand:       brand,
			Price:       float64(10 + (i % 990)),
		}
		
		productStore.Store(i, product)
		productIDs[i-1] = i
	}
	
	elapsed := time.Since(start)
	log.Printf("✅ Generated 100,000 products in %s", elapsed)
}

// Redis Helper Functions with Circuit Breaker and Retry

// ErrRedisSimulatedFailure is used for testing failure scenarios
var ErrRedisSimulatedFailure = errors.New("simulated Redis failure")

// checkRedisFailureSimulation checks if failure simulation is enabled
func checkRedisFailureSimulation() error {
	failureSimMutex.RLock()
	defer failureSimMutex.RUnlock()
	if simulateRedisFailure {
		return ErrRedisSimulatedFailure
	}
	return nil
}

// cacheHotProduct caches a product in Redis with circuit breaker
func cacheHotProduct(ctx context.Context, product Product) error {
	if !redisEnabled {
		return nil
	}
	
	atomic.AddInt64(&totalRedisWrites, 1)
	
	retryConfig := RetryConfig{
		MaxRetries:    2,
		InitialDelay:  50 * time.Millisecond,
		MaxDelay:      500 * time.Millisecond,
		BackoffFactor: 2.0,
		Jitter:        true,
	}
	
	result := RetryWithBackoff(ctx, retryConfig, "redis-cache-product", func() error {
		// Check for simulated failure
		if err := checkRedisFailureSimulation(); err != nil {
			return err
		}
		
		return redisCircuitBreaker.Execute(func() error {
			data, err := json.Marshal(product)
			if err != nil {
				return fmt.Errorf("failed to marshal product: %w", err)
			}
			
			key := fmt.Sprintf("%s%d", hotProductKeyPrefix, product.ID)
			return redisClient.Set(ctx, key, data, hotProductTTL).Err()
		})
	})
	
	if !result.Success {
		return result.FinalError
	}
	return nil
}

// getHotProduct retrieves a product from Redis cache with circuit breaker
func getHotProduct(ctx context.Context, productID int) (*Product, error) {
	if !redisEnabled {
		return nil, nil
	}
	
	var product *Product
	
	// For cache reads, we use fewer retries since cache misses are acceptable
	retryConfig := RetryConfig{
		MaxRetries:    1,
		InitialDelay:  50 * time.Millisecond,
		MaxDelay:      200 * time.Millisecond,
		BackoffFactor: 2.0,
		Jitter:        true,
	}
	
	result := RetryWithBackoff(ctx, retryConfig, "redis-get-product", func() error {
		// Check for simulated failure
		if err := checkRedisFailureSimulation(); err != nil {
			return err
		}
		
		return redisCircuitBreaker.Execute(func() error {
			key := fmt.Sprintf("%s%d", hotProductKeyPrefix, productID)
			data, err := redisClient.Get(ctx, key).Bytes()
			if err == redis.Nil {
				atomic.AddInt64(&totalRedisMisses, 1)
				product = nil
				return nil // Cache miss is not an error
			}
			if err != nil {
				return fmt.Errorf("redis get error: %w", err)
			}
			
			atomic.AddInt64(&totalRedisHits, 1)
			
			var p Product
			if err := json.Unmarshal(data, &p); err != nil {
				return fmt.Errorf("failed to unmarshal product: %w", err)
			}
			product = &p
			return nil
		})
	})
	
	if !result.Success {
		// On Redis failure, return nil to fall back to memory
		log.Printf("⚠️ Redis get failed, falling back to memory: %v", result.FinalError)
		return nil, nil
	}
	return product, nil
}

// getAllHotProducts retrieves all hot products from Redis with circuit breaker
func getAllHotProducts(ctx context.Context) ([]Product, error) {
	if !redisEnabled {
		return nil, nil
	}
	
	var products []Product
	
	retryConfig := RetryConfig{
		MaxRetries:    2,
		InitialDelay:  50 * time.Millisecond,
		MaxDelay:      500 * time.Millisecond,
		BackoffFactor: 2.0,
		Jitter:        true,
	}
	
	result := RetryWithBackoff(ctx, retryConfig, "redis-get-all-hot", func() error {
		// Check for simulated failure
		if err := checkRedisFailureSimulation(); err != nil {
			return err
		}
		
		return redisCircuitBreaker.Execute(func() error {
			// Find all hot product keys
			pattern := hotProductKeyPrefix + "*"
			keys, err := redisClient.Keys(ctx, pattern).Result()
			if err != nil {
				return fmt.Errorf("failed to get hot product keys: %w", err)
			}
			
			if len(keys) == 0 {
				products = []Product{}
				return nil
			}
			
			// Get all products
			values, err := redisClient.MGet(ctx, keys...).Result()
			if err != nil {
				return fmt.Errorf("failed to get hot products: %w", err)
			}
			
			products = make([]Product, 0, len(values))
			for _, val := range values {
				if val == nil {
					continue
				}
				var product Product
				if err := json.Unmarshal([]byte(val.(string)), &product); err != nil {
					continue
				}
				products = append(products, product)
			}
			return nil
		})
	})
	
	if !result.Success {
		return nil, result.FinalError
	}
	return products, nil
}

// DynamoDB Helper Functions with Circuit Breaker and Retry

// ErrDynamoDBSimulatedFailure is used for testing failure scenarios
var ErrDynamoDBSimulatedFailure = errors.New("simulated DynamoDB failure")

// checkDynamoDBFailureSimulation checks if failure simulation is enabled
func checkDynamoDBFailureSimulation() error {
	failureSimMutex.RLock()
	defer failureSimMutex.RUnlock()
	if simulateDynamoDBFailure {
		return ErrDynamoDBSimulatedFailure
	}
	return nil
}

func getCartFromDynamoDB(ctx context.Context, userID string) (*Cart, error) {
	atomic.AddInt64(&totalDynamoDBReads, 1)
	
	var cart *Cart
	retryConfig := DefaultRetryConfig()
	
	result := RetryWithBackoff(ctx, retryConfig, "dynamodb-get-cart", func() error {
		// Check for simulated failure
		if err := checkDynamoDBFailureSimulation(); err != nil {
			return err
		}
		
		return dynamoDBCircuitBreaker.Execute(func() error {
			resp, err := dynamoClient.GetItem(ctx, &dynamodb.GetItemInput{
				TableName: aws.String(cartsTableName),
				Key: map[string]types.AttributeValue{
					"user_id": &types.AttributeValueMemberS{Value: userID},
				},
			})
			if err != nil {
				return fmt.Errorf("failed to get cart: %w", err)
			}
			
			if resp.Item == nil {
				cart = nil
				return nil
			}
			
			var c Cart
			if err := attributevalue.UnmarshalMap(resp.Item, &c); err != nil {
				return fmt.Errorf("failed to unmarshal cart: %w", err)
			}
			cart = &c
			return nil
		})
	})
	
	if !result.Success {
		return nil, result.FinalError
	}
	return cart, nil
}

func saveCartToDynamoDB(ctx context.Context, cart *Cart) error {
	atomic.AddInt64(&totalDynamoDBWrites, 1)
	
	cart.TTL = time.Now().Add(7 * 24 * time.Hour).Unix()
	cart.UpdatedAt = time.Now().Format(time.RFC3339)
	
	retryConfig := DefaultRetryConfig()
	
	result := RetryWithBackoff(ctx, retryConfig, "dynamodb-save-cart", func() error {
		// Check for simulated failure
		if err := checkDynamoDBFailureSimulation(); err != nil {
			return err
		}
		
		return dynamoDBCircuitBreaker.Execute(func() error {
			item, err := attributevalue.MarshalMap(cart)
			if err != nil {
				return fmt.Errorf("failed to marshal cart: %w", err)
			}
			
			_, err = dynamoClient.PutItem(ctx, &dynamodb.PutItemInput{
				TableName: aws.String(cartsTableName),
				Item:      item,
			})
			if err != nil {
				return fmt.Errorf("failed to save cart: %w", err)
			}
			return nil
		})
	})
	
	if !result.Success {
		return result.FinalError
	}
	return nil
}

func deleteCartFromDynamoDB(ctx context.Context, userID string) error {
	atomic.AddInt64(&totalDynamoDBWrites, 1)
	
	retryConfig := DefaultRetryConfig()
	
	result := RetryWithBackoff(ctx, retryConfig, "dynamodb-delete-cart", func() error {
		// Check for simulated failure
		if err := checkDynamoDBFailureSimulation(); err != nil {
			return err
		}
		
		return dynamoDBCircuitBreaker.Execute(func() error {
			_, err := dynamoClient.DeleteItem(ctx, &dynamodb.DeleteItemInput{
				TableName: aws.String(cartsTableName),
				Key: map[string]types.AttributeValue{
					"user_id": &types.AttributeValueMemberS{Value: userID},
				},
			})
			return err
		})
	})
	
	if !result.Success {
		return result.FinalError
	}
	return nil
}

func saveOrderToDynamoDB(ctx context.Context, order *Order) error {
	atomic.AddInt64(&totalDynamoDBWrites, 1)
	
	retryConfig := DefaultRetryConfig()
	
	result := RetryWithBackoff(ctx, retryConfig, "dynamodb-save-order", func() error {
		// Check for simulated failure
		if err := checkDynamoDBFailureSimulation(); err != nil {
			return err
		}
		
		return dynamoDBCircuitBreaker.Execute(func() error {
			item, err := attributevalue.MarshalMap(order)
			if err != nil {
				return fmt.Errorf("failed to marshal order: %w", err)
			}
			
			_, err = dynamoClient.PutItem(ctx, &dynamodb.PutItemInput{
				TableName: aws.String(ordersTableName),
				Item:      item,
			})
			if err != nil {
				return fmt.Errorf("failed to save order: %w", err)
			}
			return nil
		})
	})
	
	if !result.Success {
		return result.FinalError
	}
	return nil
}

func getOrderFromDynamoDB(ctx context.Context, orderID string) (*Order, error) {
	atomic.AddInt64(&totalDynamoDBReads, 1)
	
	var order *Order
	retryConfig := DefaultRetryConfig()
	
	result := RetryWithBackoff(ctx, retryConfig, "dynamodb-get-order", func() error {
		// Check for simulated failure
		if err := checkDynamoDBFailureSimulation(); err != nil {
			return err
		}
		
		return dynamoDBCircuitBreaker.Execute(func() error {
			resp, err := dynamoClient.GetItem(ctx, &dynamodb.GetItemInput{
				TableName: aws.String(ordersTableName),
				Key: map[string]types.AttributeValue{
					"order_id": &types.AttributeValueMemberS{Value: orderID},
				},
			})
			if err != nil {
				return fmt.Errorf("failed to get order: %w", err)
			}
			
			if resp.Item == nil {
				order = nil
				return nil
			}
			
			var o Order
			if err := attributevalue.UnmarshalMap(resp.Item, &o); err != nil {
				return fmt.Errorf("failed to unmarshal order: %w", err)
			}
			order = &o
			return nil
		})
	})
	
	if !result.Success {
		return nil, result.FinalError
	}
	return order, nil
}

// HTTP Handlers

// searchProducts performs bounded iteration search
func searchProducts(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	
	atomic.AddInt64(&totalRequestsProcessed, 1)
	atomic.AddInt64(&totalReadOperations, 1)
	
	query := strings.ToLower(r.URL.Query().Get("q"))
	if query == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "Query parameter 'q' is required",
		})
		return
	}

	var results []Product
	totalFound := 0
	checkedCount := 0
	
	const maxChecks = 100
	const maxResults = 20
	
	startIdx := 0
	
	for i := 0; i < maxChecks && i < len(productIDs); i++ {
		idx := (startIdx + i) % len(productIDs)
		productID := productIDs[idx]
		
		checkedCount++
		
		if val, ok := productStore.Load(productID); ok {
			product := val.(Product)
			
			nameMatch := strings.Contains(strings.ToLower(product.Name), query)
			categoryMatch := strings.Contains(strings.ToLower(product.Category), query)
			
			if nameMatch || categoryMatch {
				totalFound++
				
				if len(results) < maxResults {
					results = append(results, product)
				}
			}
		}
	}
	
	searchTime := time.Since(startTime)
	
	response := SearchResponse{
		Products:     results,
		TotalFound:   totalFound,
		SearchTime:   fmt.Sprintf("%.3fs", searchTime.Seconds()),
		CheckedCount: checkedCount,
	}
	
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// getHotProducts returns all cached hot products from Redis
func getHotProducts(w http.ResponseWriter, r *http.Request) {
	atomic.AddInt64(&totalReadOperations, 1)
	
	if !redisEnabled {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "Redis not enabled",
			"hint":  "Set REDIS_ENDPOINT environment variable",
		})
		return
	}
	
	startTime := time.Now()
	products, err := getAllHotProducts(r.Context())
	elapsed := time.Since(startTime)
	
	if err != nil {
		log.Printf("Error getting hot products: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to get hot products"})
		return
	}
	
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"hot_products": products,
		"count":        len(products),
		"latency":      fmt.Sprintf("%.3fms", float64(elapsed.Microseconds())/1000),
		"source":       "redis",
	})
}

// markProductHot caches a product as "hot" in Redis
func markProductHot(w http.ResponseWriter, r *http.Request) {
	atomic.AddInt64(&totalWriteOperations, 1)
	
	vars := mux.Vars(r)
	productID := vars["id"]
	
	id, err := strconv.Atoi(productID)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid product ID"})
		return
	}
	
	// Get product from in-memory store
	val, ok := productStore.Load(id)
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "Product not found"})
		return
	}
	product := val.(Product)
	
	if !redisEnabled {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "Redis not enabled",
			"hint":  "Set REDIS_ENDPOINT environment variable",
		})
		return
	}
	
	// Cache in Redis
	if err := cacheHotProduct(r.Context(), product); err != nil {
		log.Printf("Error caching hot product: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to cache product"})
		return
	}
	
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message":    "Product marked as hot",
		"product_id": id,
		"ttl":        hotProductTTL.String(),
		"cached_in":  "redis",
	})
}

// compareProductLatency compares Redis vs in-memory latency for a product
func compareProductLatency(w http.ResponseWriter, r *http.Request) {
	atomic.AddInt64(&totalReadOperations, 1)
	
	vars := mux.Vars(r)
	productID := vars["id"]
	
	id, err := strconv.Atoi(productID)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid product ID"})
		return
	}
	
	// Measure in-memory latency
	memStart := time.Now()
	val, ok := productStore.Load(id)
	memLatency := time.Since(memStart)
	
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "Product not found"})
		return
	}
	product := val.(Product)
	
	// Measure Redis latency
	var redisLatency time.Duration
	var cacheHit bool
	
	if redisEnabled {
		redisStart := time.Now()
		cachedProduct, err := getHotProduct(r.Context(), id)
		redisLatency = time.Since(redisStart)
		
		if err == nil && cachedProduct != nil {
			cacheHit = true
		}
	}
	
	response := CompareResponse{
		ProductID:     id,
		Product:       product,
		MemoryLatency: fmt.Sprintf("%.3fms", float64(memLatency.Microseconds())/1000),
		CacheHit:      cacheHit,
	}
	
	if redisEnabled {
		response.RedisLatency = fmt.Sprintf("%.3fms", float64(redisLatency.Microseconds())/1000)
		if memLatency > 0 && redisLatency > 0 {
			response.SpeedupFactor = float64(memLatency) / float64(redisLatency)
		}
	} else {
		response.RedisLatency = "N/A (Redis disabled)"
	}
	
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// healthCheck handles health check with memory threshold monitoring
func healthCheck(w http.ResponseWriter, r *http.Request) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	memoryMB := m.Alloc / 1024 / 1024
	
	const memoryThresholdMB = 1500
	
	w.Header().Set("Content-Type", "application/json")
	
	if memoryMB > memoryThresholdMB {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":       "unhealthy",
			"reason":       "memory threshold exceeded",
			"memory_mb":    memoryMB,
			"threshold_mb": memoryThresholdMB,
			"action":       "task will be restarted by ECS",
		})
		log.Printf("🏥 Health check FAILED - Memory: %d MB (threshold: %d MB)", memoryMB, memoryThresholdMB)
		return
	}
	
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":    "healthy",
		"memory_mb": memoryMB,
		"dynamodb":  useDynamoDB,
		"redis":     redisEnabled,
	})
}

// statsEndpoint shows system statistics
func statsEndpoint(w http.ResponseWriter, r *http.Request) {
	count := 0
	productStore.Range(func(key, value interface{}) bool {
		count++
		return true
	})
	
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"total_products":   count,
		"memory_footprint": "~100,000 products",
		"dynamodb_enabled": useDynamoDB,
		"redis_enabled":    redisEnabled,
		"redis_endpoint":   redisEndpoint,
		"products_table":   productsTableName,
		"carts_table":      cartsTableName,
		"orders_table":     ordersTableName,
	})
}

// memoryStatsEndpoint shows current memory and operation stats
func memoryStatsEndpoint(w http.ResponseWriter, r *http.Request) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"alloc_mb":           m.Alloc / 1024 / 1024,
		"total_alloc_mb":     m.TotalAlloc / 1024 / 1024,
		"sys_mb":             m.Sys / 1024 / 1024,
		"num_gc":             m.NumGC,
		"requests_processed": totalRequestsProcessed,
		"read_operations":    totalReadOperations,
		"write_operations":   totalWriteOperations,
		"dynamodb_reads":     totalDynamoDBReads,
		"dynamodb_writes":    totalDynamoDBWrites,
		"redis_hits":         totalRedisHits,
		"redis_misses":       totalRedisMisses,
		"redis_writes":       totalRedisWrites,
	})
}

// getProduct retrieves a single product by ID
func getProduct(w http.ResponseWriter, r *http.Request) {
	atomic.AddInt64(&totalReadOperations, 1)
	
	vars := mux.Vars(r)
	productID := vars["id"]
	
	var id int
	_, err := fmt.Sscanf(productID, "%d", &id)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid product ID"})
		return
	}
	
	// Try Redis cache first (for hot products)
	if redisEnabled {
		if cachedProduct, err := getHotProduct(r.Context(), id); err == nil && cachedProduct != nil {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Cache", "HIT")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(cachedProduct)
			return
		}
	}
	
	// Fallback to in-memory store
	if val, ok := productStore.Load(id); ok {
		product := val.(Product)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Cache", "MISS")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(product)
	} else {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "Product not found"})
	}
}

// getCart retrieves user's cart
func getCart(w http.ResponseWriter, r *http.Request) {
	atomic.AddInt64(&totalReadOperations, 1)
	
	vars := mux.Vars(r)
	userID := vars["userId"]
	
	var cart *Cart
	var err error
	
	if useDynamoDB {
		cart, err = getCartFromDynamoDB(r.Context(), userID)
		if err != nil {
			log.Printf("Error getting cart from DynamoDB: %v", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "Failed to retrieve cart"})
			return
		}
	} else {
		if val, ok := cartStore.Load(userID); ok {
			c := val.(Cart)
			cart = &c
		}
	}
	
	if cart == nil {
		cart = &Cart{
			UserID:    userID,
			Items:     []CartItem{},
			Total:     0,
			UpdatedAt: time.Now().Format(time.RFC3339),
		}
	}
	
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(cart)
}

// addToCart adds an item to cart
func addToCart(w http.ResponseWriter, r *http.Request) {
	atomic.AddInt64(&totalWriteOperations, 1)
	
	vars := mux.Vars(r)
	userID := vars["userId"]
	
	var req AddToCartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid request body"})
		return
	}
	
	if req.Quantity <= 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Quantity must be positive"})
		return
	}
	
	val, ok := productStore.Load(req.ProductID)
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "Product not found"})
		return
	}
	product := val.(Product)
	
	var cart *Cart
	
	if useDynamoDB {
		var err error
		cart, err = getCartFromDynamoDB(r.Context(), userID)
		if err != nil {
			log.Printf("Error getting cart: %v", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "Failed to retrieve cart"})
			return
		}
	} else {
		if val, ok := cartStore.Load(userID); ok {
			c := val.(Cart)
			cart = &c
		}
	}
	
	if cart == nil {
		cart = &Cart{
			UserID: userID,
			Items:  []CartItem{},
			Total:  0,
		}
	}
	
	found := false
	for i, item := range cart.Items {
		if item.ProductID == req.ProductID {
			cart.Items[i].Quantity += req.Quantity
			found = true
			break
		}
	}
	
	if !found {
		cart.Items = append(cart.Items, CartItem{
			ProductID: req.ProductID,
			Quantity:  req.Quantity,
			Price:     product.Price,
		})
	}
	
	cart.Total = 0
	for _, item := range cart.Items {
		cart.Total += item.Price * float64(item.Quantity)
	}
	cart.UpdatedAt = time.Now().Format(time.RFC3339)
	
	if useDynamoDB {
		if err := saveCartToDynamoDB(r.Context(), cart); err != nil {
			log.Printf("Error saving cart: %v", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "Failed to save cart"})
			return
		}
	} else {
		cartStore.Store(userID, *cart)
	}
	
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(cart)
}

// updateCartItem updates item quantity in cart
func updateCartItem(w http.ResponseWriter, r *http.Request) {
	atomic.AddInt64(&totalWriteOperations, 1)
	
	vars := mux.Vars(r)
	userID := vars["userId"]
	productID := vars["productId"]
	
	id, err := strconv.Atoi(productID)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid product ID"})
		return
	}
	
	var req struct {
		Quantity int `json:"quantity"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid request body"})
		return
	}
	
	var cart *Cart
	
	if useDynamoDB {
		cart, err = getCartFromDynamoDB(r.Context(), userID)
		if err != nil {
			log.Printf("Error getting cart: %v", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "Failed to retrieve cart"})
			return
		}
	} else {
		if val, ok := cartStore.Load(userID); ok {
			c := val.(Cart)
			cart = &c
		}
	}
	
	if cart == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "Cart not found"})
		return
	}
	
	if req.Quantity <= 0 {
		newItems := []CartItem{}
		for _, item := range cart.Items {
			if item.ProductID != id {
				newItems = append(newItems, item)
			}
		}
		cart.Items = newItems
	} else {
		found := false
		for i, item := range cart.Items {
			if item.ProductID == id {
				cart.Items[i].Quantity = req.Quantity
				found = true
				break
			}
		}
		if !found {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": "Item not in cart"})
			return
		}
	}
	
	cart.Total = 0
	for _, item := range cart.Items {
		cart.Total += item.Price * float64(item.Quantity)
	}
	cart.UpdatedAt = time.Now().Format(time.RFC3339)
	
	if useDynamoDB {
		if err := saveCartToDynamoDB(r.Context(), cart); err != nil {
			log.Printf("Error saving cart: %v", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "Failed to save cart"})
			return
		}
	} else {
		cartStore.Store(userID, *cart)
	}
	
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(cart)
}

// CheckoutResponse includes order and payment information
type CheckoutResponse struct {
	Order           Order            `json:"order"`
	Payment         *PaymentResponse `json:"payment,omitempty"`
	RecoveryApplied bool             `json:"recovery_applied"`
	RecoveryDetails string           `json:"recovery_details,omitempty"`
}

// checkout creates an order from cart with payment processing
func checkout(w http.ResponseWriter, r *http.Request) {
	atomic.AddInt64(&totalWriteOperations, 1)
	
	vars := mux.Vars(r)
	userID := vars["userId"]
	
	var cart *Cart
	var err error
	
	if useDynamoDB {
		cart, err = getCartFromDynamoDB(r.Context(), userID)
		if err != nil {
			log.Printf("Error getting cart: %v", err)
			
			// Check if it's a circuit breaker error
			if errors.Is(err, ErrCircuitOpen) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusServiceUnavailable)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"error":            "Database temporarily unavailable",
					"circuit_breaker":  "open",
					"retry_after_secs": 30,
				})
				return
			}
			
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "Failed to retrieve cart"})
			return
		}
	} else {
		if val, ok := cartStore.Load(userID); ok {
			c := val.(Cart)
			cart = &c
		}
	}
	
	if cart == nil || len(cart.Items) == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Cart is empty"})
		return
	}
	
	orderID := fmt.Sprintf("ORD-%s-%d", userID, time.Now().UnixNano())
	order := Order{
		OrderID:   orderID,
		UserID:    userID,
		Items:     cart.Items,
		Total:     cart.Total,
		Status:    "pending_payment",
		CreatedAt: time.Now().Format(time.RFC3339),
	}
	
	// Process payment with retry and circuit breaker
	paymentReq := PaymentRequest{
		OrderID:    orderID,
		Amount:     cart.Total,
		UserID:     userID,
		CardNumber: "****1234", // Simulated
	}
	
	response := CheckoutResponse{
		Order: order,
	}
	
	paymentResp, paymentErr := paymentService.ProcessPayment(r.Context(), paymentReq)
	response.Payment = paymentResp
	
	if paymentErr != nil {
		log.Printf("Payment failed for order %s: %v", orderID, paymentErr)
		
		// Check if circuit breaker is open
		if errors.Is(paymentErr, ErrCircuitOpen) {
			order.Status = "payment_service_unavailable"
			response.Order = order
			response.RecoveryApplied = true
			response.RecoveryDetails = "Payment service circuit breaker is open. Order saved for retry."
			
			// Save order with pending status for later retry
			if useDynamoDB {
				saveOrderToDynamoDB(r.Context(), &order)
			} else {
				orderStore.Store(orderID, order)
			}
			
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(response)
			return
		}
		
		// Payment failed after retries
		order.Status = "payment_failed"
		response.Order = order
		response.RecoveryApplied = paymentResp != nil && paymentResp.RetryInfo != nil && paymentResp.RetryInfo.Attempts > 1
		if response.RecoveryApplied {
			response.RecoveryDetails = fmt.Sprintf("Retry pattern applied: %d attempts made", paymentResp.RetryInfo.Attempts)
		}
		
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPaymentRequired)
		json.NewEncoder(w).Encode(response)
		return
	}
	
	// Payment successful
	order.Status = "confirmed"
	response.Order = order
	response.RecoveryApplied = paymentResp.RetryInfo != nil && paymentResp.RetryInfo.Attempts > 1
	if response.RecoveryApplied {
		response.RecoveryDetails = fmt.Sprintf("Payment succeeded after %d attempts (retry pattern applied)", paymentResp.RetryInfo.Attempts)
	}
	
	if useDynamoDB {
		if err := saveOrderToDynamoDB(r.Context(), &order); err != nil {
			log.Printf("Error saving order: %v", err)
			
			if errors.Is(err, ErrCircuitOpen) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusServiceUnavailable)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"error":            "Database temporarily unavailable",
					"circuit_breaker":  "open",
					"payment_status":   "success",
					"payment_id":       paymentResp.TransactionID,
					"retry_after_secs": 30,
				})
				return
			}
			
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "Failed to create order"})
			return
		}
		
		if err := deleteCartFromDynamoDB(r.Context(), userID); err != nil {
			log.Printf("Warning: Failed to clear cart after checkout: %v", err)
		}
	} else {
		orderStore.Store(orderID, order)
		cartStore.Delete(userID)
	}
	
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(response)
}

// ============================================================================
// FAILURE SIMULATION & TESTING ENDPOINTS
// ============================================================================

// simulateFailureHandler configures failure simulation
func simulateFailureHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	service := vars["service"]
	
	var req struct {
		Enable      bool    `json:"enable"`
		FailureRate float64 `json:"failure_rate,omitempty"` // For payment service
		LatencyMs   int     `json:"latency_ms,omitempty"`   // For payment service
	}
	
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid request body"})
		return
	}
	
	response := map[string]interface{}{
		"service": service,
		"enabled": req.Enable,
	}
	
	switch service {
	case "dynamodb":
		failureSimMutex.Lock()
		simulateDynamoDBFailure = req.Enable
		failureSimMutex.Unlock()
		response["message"] = fmt.Sprintf("DynamoDB failure simulation %s", enabledStr(req.Enable))
		log.Printf("🔧 DynamoDB failure simulation: %s", enabledStr(req.Enable))
		
	case "redis":
		failureSimMutex.Lock()
		simulateRedisFailure = req.Enable
		failureSimMutex.Unlock()
		response["message"] = fmt.Sprintf("Redis failure simulation %s", enabledStr(req.Enable))
		log.Printf("🔧 Redis failure simulation: %s", enabledStr(req.Enable))
		
	case "payment":
		if req.Enable {
			// Set failure rate (default 80% if not specified)
			rate := req.FailureRate
			if rate == 0 {
				rate = 0.8
			}
			paymentService.SetFailureRate(rate)
			response["failure_rate"] = rate
		} else {
			paymentService.SetFailureRate(0)
		}
		if req.LatencyMs > 0 {
			paymentService.SetLatency(req.LatencyMs)
			response["latency_ms"] = req.LatencyMs
		}
		response["message"] = fmt.Sprintf("Payment failure simulation %s", enabledStr(req.Enable))
		log.Printf("🔧 Payment failure simulation: %s (rate: %.1f%%)", enabledStr(req.Enable), req.FailureRate*100)
		
	default:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error":            "Unknown service",
			"valid_services":   "dynamodb, redis, payment",
		})
		return
	}
	
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

func enabledStr(enabled bool) string {
	if enabled {
		return "ENABLED"
	}
	return "DISABLED"
}

// circuitBreakerStatusHandler returns circuit breaker status
func circuitBreakerStatusHandler(w http.ResponseWriter, r *http.Request) {
	status := map[string]interface{}{
		"timestamp": time.Now().Format(time.RFC3339),
	}
	
	if dynamoDBCircuitBreaker != nil {
		status["dynamodb"] = dynamoDBCircuitBreaker.GetStats()
	} else {
		status["dynamodb"] = "not initialized"
	}
	
	if redisCircuitBreaker != nil {
		status["redis"] = redisCircuitBreaker.GetStats()
	} else {
		status["redis"] = "not initialized"
	}
	
	if paymentService != nil {
		status["payment"] = paymentService.GetStats()
	} else {
		status["payment"] = "not initialized"
	}
	
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(status)
}

// resetCircuitBreakerHandler resets a circuit breaker
func resetCircuitBreakerHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	service := vars["service"]
	
	response := map[string]interface{}{
		"service": service,
	}
	
	switch service {
	case "dynamodb":
		if dynamoDBCircuitBreaker != nil {
			dynamoDBCircuitBreaker.Reset()
			response["message"] = "DynamoDB circuit breaker reset"
			response["new_state"] = dynamoDBCircuitBreaker.GetState().String()
		} else {
			response["error"] = "DynamoDB circuit breaker not initialized"
		}
		
	case "redis":
		if redisCircuitBreaker != nil {
			redisCircuitBreaker.Reset()
			response["message"] = "Redis circuit breaker reset"
			response["new_state"] = redisCircuitBreaker.GetState().String()
		} else {
			response["error"] = "Redis circuit breaker not initialized"
		}
		
	case "payment":
		if paymentService != nil {
			paymentService.ResetCircuitBreaker()
			response["message"] = "Payment circuit breaker reset"
			response["new_state"] = paymentService.circuitBreaker.GetState().String()
		} else {
			response["error"] = "Payment service not initialized"
		}
		
	case "all":
		if dynamoDBCircuitBreaker != nil {
			dynamoDBCircuitBreaker.Reset()
		}
		if redisCircuitBreaker != nil {
			redisCircuitBreaker.Reset()
		}
		if paymentService != nil {
			paymentService.ResetCircuitBreaker()
		}
		response["message"] = "All circuit breakers reset"
		
	default:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error":          "Unknown service",
			"valid_services": "dynamodb, redis, payment, all",
		})
		return
	}
	
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// testRetryHandler demonstrates retry pattern
func testRetryHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FailCount  int `json:"fail_count"`  // Number of times to fail before succeeding
		MaxRetries int `json:"max_retries"` // Max retries to configure
	}
	
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid request body"})
		return
	}
	
	if req.MaxRetries == 0 {
		req.MaxRetries = 3
	}
	
	attemptCount := 0
	
	config := RetryConfig{
		MaxRetries:    req.MaxRetries,
		InitialDelay:  100 * time.Millisecond,
		MaxDelay:      1 * time.Second,
		BackoffFactor: 2.0,
		Jitter:        true,
	}
	
	result := RetryWithBackoff(r.Context(), config, "test-retry", func() error {
		attemptCount++
		if attemptCount <= req.FailCount {
			return fmt.Errorf("simulated failure #%d", attemptCount)
		}
		return nil
	})
	
	response := map[string]interface{}{
		"configured_fail_count": req.FailCount,
		"configured_max_retries": req.MaxRetries,
		"actual_attempts":       result.Attempts,
		"success":               result.Success,
		"total_time":            result.TotalTime.String(),
	}
	
	if result.Success {
		response["message"] = fmt.Sprintf("Operation succeeded after %d attempts", result.Attempts)
	} else {
		response["message"] = fmt.Sprintf("Operation failed after %d attempts", result.Attempts)
		response["error"] = result.ErrorMessage
	}
	
	w.Header().Set("Content-Type", "application/json")
	if result.Success {
		w.WriteHeader(http.StatusOK)
	} else {
		w.WriteHeader(http.StatusInternalServerError)
	}
	json.NewEncoder(w).Encode(response)
}

// failureRecoveryDemoHandler provides a comprehensive demo endpoint
func failureRecoveryDemoHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"title": "Failure Recovery Demo - Circuit Breaker & Retry Patterns",
		"description": "This API implements failure recovery using Circuit Breaker and Retry patterns",
		"patterns": map[string]interface{}{
			"circuit_breaker": map[string]interface{}{
				"description": "Prevents cascading failures by temporarily stopping requests to failing services",
				"states": []string{"CLOSED (normal)", "OPEN (failing)", "HALF_OPEN (testing)"},
				"configuration": map[string]interface{}{
					"failure_threshold": "5 failures to open circuit",
					"success_threshold": "3 successes to close from half-open",
					"timeout":           "30 seconds before testing recovery",
				},
			},
			"retry": map[string]interface{}{
				"description": "Retries failed operations with exponential backoff",
				"configuration": map[string]interface{}{
					"max_retries":    3,
					"initial_delay":  "100ms",
					"max_delay":      "5s",
					"backoff_factor": 2.0,
					"jitter":         true,
				},
			},
		},
		"demo_steps": []map[string]interface{}{
			{
				"step":        1,
				"description": "Check current circuit breaker status",
				"endpoint":    "GET /resilience/circuit-breakers",
			},
			{
				"step":        2,
				"description": "Enable payment failure simulation (80% failure rate)",
				"endpoint":    "POST /resilience/simulate/payment",
				"body":        `{"enable": true, "failure_rate": 0.8}`,
			},
			{
				"step":        3,
				"description": "Attempt checkout - watch retry pattern in action",
				"endpoint":    "POST /cart/{userId}/checkout",
			},
			{
				"step":        4,
				"description": "Make multiple failed requests to trip circuit breaker",
				"note":        "After 3-5 failures, circuit will OPEN",
			},
			{
				"step":        5,
				"description": "Check circuit breaker status - should be OPEN",
				"endpoint":    "GET /resilience/circuit-breakers",
			},
			{
				"step":        6,
				"description": "Wait 30 seconds and retry - circuit will be HALF_OPEN",
				"note":        "Or reset manually: POST /resilience/reset/payment",
			},
			{
				"step":        7,
				"description": "Disable failure simulation",
				"endpoint":    "POST /resilience/simulate/payment",
				"body":        `{"enable": false}`,
			},
			{
				"step":        8,
				"description": "Successful checkout - circuit will close",
				"endpoint":    "POST /cart/{userId}/checkout",
			},
		},
		"endpoints": map[string]interface{}{
			"simulate_failures": map[string]string{
				"POST /resilience/simulate/dynamodb": "Enable/disable DynamoDB failures",
				"POST /resilience/simulate/redis":    "Enable/disable Redis failures",
				"POST /resilience/simulate/payment":  "Enable/disable Payment failures with rate",
			},
			"circuit_breaker_management": map[string]string{
				"GET /resilience/circuit-breakers":    "View all circuit breaker status",
				"POST /resilience/reset/{service}":    "Reset circuit breaker (dynamodb|redis|payment|all)",
			},
			"testing": map[string]string{
				"POST /resilience/test-retry": "Test retry pattern with configurable failures",
			},
		},
	})
}

// getOrder retrieves an order
func getOrder(w http.ResponseWriter, r *http.Request) {
	atomic.AddInt64(&totalReadOperations, 1)
	
	vars := mux.Vars(r)
	orderID := vars["orderId"]
	
	var order *Order
	var err error
	
	if useDynamoDB {
		order, err = getOrderFromDynamoDB(r.Context(), orderID)
		if err != nil {
			log.Printf("Error getting order: %v", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "Failed to retrieve order"})
			return
		}
	} else {
		if val, ok := orderStore.Load(orderID); ok {
			o := val.(Order)
			order = &o
		}
	}
	
	if order == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "Order not found"})
		return
	}
	
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(order)
}

func main() {
	// Initialize clients
	initDynamoDB()
	initRedis()
	initPaymentService()
	
	// Initialize fallback circuit breakers if not yet created
	if dynamoDBCircuitBreaker == nil {
		dynamoDBCircuitBreaker = NewCircuitBreaker("dynamodb", 5, 3, 30*time.Second)
	}
	if redisCircuitBreaker == nil {
		redisCircuitBreaker = NewCircuitBreaker("redis", 5, 3, 30*time.Second)
	}
	
	// Initialize 100,000 products at startup
	initializeProducts()
	
	router := mux.NewRouter()

	// READ endpoints
	router.HandleFunc("/products/search", searchProducts).Methods("GET")
	router.HandleFunc("/products/hot", getHotProducts).Methods("GET")
	router.HandleFunc("/products/{id}", getProduct).Methods("GET")
	router.HandleFunc("/products/{id}/compare", compareProductLatency).Methods("GET")
	router.HandleFunc("/cart/{userId}", getCart).Methods("GET")
	router.HandleFunc("/orders/{orderId}", getOrder).Methods("GET")
	
	// WRITE endpoints
	router.HandleFunc("/products/{id}/hot", markProductHot).Methods("POST")
	router.HandleFunc("/cart/{userId}/items", addToCart).Methods("POST")
	router.HandleFunc("/cart/{userId}/items/{productId}", updateCartItem).Methods("PUT")
	router.HandleFunc("/cart/{userId}/checkout", checkout).Methods("POST")
	
	// Health check and stats endpoints
	router.HandleFunc("/health", healthCheck).Methods("GET")
	router.HandleFunc("/stats", statsEndpoint).Methods("GET")
	router.HandleFunc("/memory", memoryStatsEndpoint).Methods("GET")
	
	// Failure Recovery & Resilience endpoints
	router.HandleFunc("/resilience/demo", failureRecoveryDemoHandler).Methods("GET")
	router.HandleFunc("/resilience/circuit-breakers", circuitBreakerStatusHandler).Methods("GET")
	router.HandleFunc("/resilience/simulate/{service}", simulateFailureHandler).Methods("POST")
	router.HandleFunc("/resilience/reset/{service}", resetCircuitBreakerHandler).Methods("POST")
	router.HandleFunc("/resilience/test-retry", testRetryHandler).Methods("POST")

	port := ":8080"
	log.Printf("🚀 Starting E-Commerce API server on port %s...", port)
	log.Printf("📦 Storage: DynamoDB=%v, Redis=%v", useDynamoDB, redisEnabled)
	log.Printf("📖 READ endpoints: /products/search, /products/hot, /products/{id}, /products/{id}/compare")
	log.Printf("🔥 HOT PRODUCT endpoints: POST /products/{id}/hot, GET /products/hot")
	log.Printf("✍️  WRITE endpoints: /cart/{userId}/items, /cart/{userId}/checkout")
	log.Printf("🛡️  RESILIENCE endpoints: /resilience/demo, /resilience/circuit-breakers, /resilience/simulate/{service}")
	log.Printf("🔄 Circuit Breakers: DynamoDB, Redis, Payment (threshold=5, recovery=30s)")
	log.Fatal(http.ListenAndServe(port, router))
}
