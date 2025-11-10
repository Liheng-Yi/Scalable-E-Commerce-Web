"""
Load Testing for Product Search Service with Memory Leak
Goal: Trigger memory leak and observe system degradation/crash
"""

from locust import HttpUser, task, between, events
import random
import time


# Track memory growth
memory_snapshots = []
start_time = None


@events.test_start.add_listener
def on_test_start(environment, **kwargs):
    """Called when the test starts"""
    global start_time
    start_time = time.time()
    print("\n" + "="*60)
    print("🔥 MEMORY LEAK TEST STARTED 🔥")
    print("="*60)
    print("This test will trigger a memory leak in the search endpoint.")
    print("Watch the memory grow until the service crashes!\n")


class ProductSearchUser(HttpUser):
    """Simulates users searching for products - triggers memory leak!"""
    
    # Wait 0.5-2 seconds between requests (aggressive load)
    wait_time = between(0.5, 2)
    
    search_terms = [
        "electronics", "books", "home", "sports",
        "alpha", "beta", "gamma", "delta", "fashion"
    ]
    
    @task(10)  # Weight: 10 (most common task)
    def search_products(self):
        """Search for products - THIS TRIGGERS THE MEMORY LEAK! 🔥
        Each call leaks 10MB of memory that never gets freed.
        """
        query = random.choice(self.search_terms)
        
        with self.client.get(
            f"/products/search?q={query}",
            name="/products/search [LEAKS 10MB]",
            catch_response=True
        ) as response:
            if response.status_code == 200:
                try:
                    data = response.json()
                    if "products" in data and "total_found" in data:
                        response.success()
                    else:
                        response.failure("Invalid response structure")
                except Exception as e:
                    response.failure(f"Failed to parse JSON: {e}")
            else:
                response.failure(f"Got status code: {response.status_code}")
    
    @task(1)  # Weight: 1 (less frequent)
    def check_memory_stats(self):
        """Monitor memory usage - shows the leak growing!"""
        with self.client.get(
            "/memory",
            name="/memory [Monitor]",
            catch_response=True
        ) as response:
            if response.status_code == 200:
                try:
                    data = response.json()
                    alloc_mb = data.get("alloc_mb", 0)
                    leaked_mb = data.get("leaked_mb_approx", 0)
                    requests = data.get("requests_processed", 0)
                    
                    # Print periodic updates (every ~50 requests)
                    if requests % 50 == 0 and requests > 0:
                        elapsed = time.time() - start_time if start_time else 0
                        print(f"\n💧 [{int(elapsed)}s] Memory Status:")
                        print(f"   - Allocated: {alloc_mb} MB")
                        print(f"   - Leaked: {leaked_mb} MB")
                        print(f"   - Requests: {requests}")
                        print(f"   - Leak Rate: {leaked_mb/max(elapsed, 1):.1f} MB/sec")
                        
                        # Warning thresholds
                        if alloc_mb > 500:
                            print(f"   ⚠️  WARNING: Memory over 500 MB!")
                        if alloc_mb > 1000:
                            print(f"   🚨 CRITICAL: Memory over 1 GB! Crash imminent!")
                    
                    memory_snapshots.append({
                        'time': time.time() - start_time if start_time else 0,
                        'alloc_mb': alloc_mb,
                        'leaked_mb': leaked_mb,
                        'requests': requests
                    })
                    
                    response.success()
                except Exception as e:
                    response.failure(f"Failed to parse memory stats: {e}")
            else:
                response.failure(f"Memory endpoint returned: {response.status_code}")
                