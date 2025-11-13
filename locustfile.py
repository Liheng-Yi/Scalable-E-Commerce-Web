"""
Basic Load Testing for E-Commerce Product Search Service
Tests all endpoints to ensure they are working correctly
"""

from locust import HttpUser, task, between, events
import random


@events.test_start.add_listener
def on_test_start(environment, **kwargs):
    """Called when the test starts"""
    print("\n" + "="*60)
    print("🚀 E-COMMERCE API LOAD TEST STARTED")
    print("="*60)
    print("Testing all endpoints: /products/search, /health, /stats, /memory")
    print("Configure users and spawn rate in the Web UI\n")


class ProductSearchUser(HttpUser):
    """Simulates users interacting with the e-commerce API"""
    
    # Wait 1-3 seconds between requests (simulates real user behavior)
    wait_time = between(1, 3)
    
    # Sample search terms for testing
    search_terms = [
        "electronics", "books", "home", "sports",
        "alpha", "beta", "gamma", "delta", 
        "fashion", "toys", "garden", "automotive"
    ]
    
    @task(10)  # Weight: 10 (most common - users searching for products)
    def search_products(self):
        """Test the product search endpoint"""
        query = random.choice(self.search_terms)
        
        with self.client.get(
            f"/products/search?q={query}",
            name="/products/search",
            catch_response=True
        ) as response:
            if response.status_code == 200:
                try:
                    data = response.json()
                    # Validate response structure
                    if "products" in data and "total_found" in data and "search_time" in data:
                        response.success()
                    else:
                        response.failure("Invalid response structure - missing required fields")
                except Exception as e:
                    response.failure(f"Failed to parse JSON: {e}")
            else:
                response.failure(f"Got status code: {response.status_code}")
    
    @task(2)  # Weight: 2 (occasional health checks)
    def check_health(self):
        """Test the health check endpoint"""
        with self.client.get(
            "/health",
            name="/health",
            catch_response=True
        ) as response:
            if response.status_code == 200:
                try:
                    data = response.json()
                    # Validate health response
                    if "status" in data and data["status"] == "healthy":
                        response.success()
                    else:
                        response.failure("Health check returned non-healthy status")
                except Exception as e:
                    response.failure(f"Failed to parse health response: {e}")
            elif response.status_code == 503:
                # Service unavailable is also a valid response (unhealthy)
                response.success()
            else:
                response.failure(f"Unexpected status code: {response.status_code}")
    
    @task(1)  # Weight: 1 (less frequent stats checks)
    def check_stats(self):
        """Test the stats endpoint"""
        with self.client.get(
            "/stats",
            name="/stats",
            catch_response=True
        ) as response:
            if response.status_code == 200:
                try:
                    data = response.json()
                    # Validate stats response
                    if "total_products" in data:
                        response.success()
                    else:
                        response.failure("Stats response missing total_products field")
                except Exception as e:
                    response.failure(f"Failed to parse stats response: {e}")
            else:
                response.failure(f"Got status code: {response.status_code}")
    
    @task(1)  # Weight: 1 (less frequent memory checks)
    # alloc_mb, total_alloc_mb, sys_mb, num_gc, requests_processed
    #  fields come from Go's runtime.MemStats structure and provide insights into  application's memory usage:
    def check_memory(self):
        """Test the memory stats endpoint"""
        with self.client.get(
            "/memory",
            name="/memory",
            catch_response=True
        ) as response:
            if response.status_code == 200:
                try:
                    data = response.json()
                    # Validate memory response
                    required_fields = ["alloc_mb", "total_alloc_mb", "sys_mb", "num_gc", "requests_processed"]
                    if all(field in data for field in required_fields):
                        response.success()
                    else:
                        response.failure("Memory response missing required fields")
                except Exception as e:
                    response.failure(f"Failed to parse memory response: {e}")
            else:
                response.failure(f"Got status code: {response.status_code}")
    
    @task(1)  # Weight: 1 (test error handling)
    def search_without_query(self):
        """Test search endpoint error handling (missing query parameter)"""
        with self.client.get(
            "/products/search",
            name="/products/search (no query)",
            catch_response=True
        ) as response:
            if response.status_code == 400:
                try:
                    data = response.json()
                    # Should return error message
                    if "error" in data:
                        response.success()
                    else:
                        response.failure("Error response missing 'error' field")
                except Exception as e:
                    response.failure(f"Failed to parse error response: {e}")
            else:
                response.failure(f"Expected 400 Bad Request, got: {response.status_code}")