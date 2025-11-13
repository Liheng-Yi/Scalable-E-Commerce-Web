f"""
READ-HEAVY Workload Test for E-Commerce API
Simulates browsing behavior: mostly product searches and views
Ratio: 90% reads, 10% writes
Tests DynamoDB read capacity and caching strategies
"""

from locust import HttpUser, task, between, events
import random


@events.test_start.add_listener
def on_test_start(environment, **kwargs):
    """Called when the test starts"""
    print("\n" + "="*60)
    print("📖 READ-HEAVY WORKLOAD TEST")
    print("="*60)
    print("Simulating browsing behavior (90% reads, 10% writes)")
    print("Focus: Testing read capacity, query performance, caching")
    print("="*60 + "\n")


class BrowsingUser(HttpUser):
    """Simulates users browsing products (read-heavy behavior)"""
    
    # Wait 1-3 seconds between requests (typical browsing)
    wait_time = between(1, 3)
    
    # Search terms for product discovery
    search_terms = [
        "electronics", "books", "home", "sports",
        "alpha", "beta", "gamma", "delta", 
        "fashion", "toys", "garden", "automotive"
    ]
    
    def on_start(self):
        """Initialize user session"""
        # Each user gets a unique ID
        self.user_id = f"user_{random.randint(1000, 9999)}"
        self.viewed_products = []
    
    @task(40)  # Weight: 40 (most common - product search)
    def search_products(self):
        """Search for products - PRIMARY READ OPERATION"""
        query = random.choice(self.search_terms)
        
        with self.client.get(
            f"/products/search?q={query}",
            name="[READ] Search Products",
            catch_response=True
        ) as response:
            if response.status_code == 200:
                try:
                    data = response.json()
                    if "products" in data and len(data["products"]) > 0:
                        # Store some product IDs for later viewing
                        for product in data["products"][:3]:
                            if product["id"] not in self.viewed_products:
                                self.viewed_products.append(product["id"])
                        response.success()
                    else:
                        response.success()  # Empty results are valid
                except Exception as e:
                    response.failure(f"Failed to parse search results: {e}")
            else:
                response.failure(f"Search failed with status: {response.status_code}")
    
    @task(30)  # Weight: 30 (view product details)
    def view_product_details(self):
        """View individual product details - READ OPERATION"""
        # View either a previously searched product or random one
        if self.viewed_products and random.random() > 0.3:
            product_id = random.choice(self.viewed_products)
        else:
            product_id = random.randint(1, 100000)
        
        with self.client.get(
            f"/products/{product_id}",
            name="[READ] View Product",
            catch_response=True
        ) as response:
            if response.status_code == 200:
                try:
                    data = response.json()
                    if "id" in data:
                        response.success()
                    else:
                        response.failure("Invalid product data")
                except Exception as e:
                    response.failure(f"Failed to parse product: {e}")
            elif response.status_code == 404:
                response.success()  # Product not found is acceptable
            else:
                response.failure(f"Unexpected status: {response.status_code}")
    
    @task(10)  # Weight: 10 (check cart)
    def view_cart(self):
        """View shopping cart - READ OPERATION"""
        with self.client.get(
            f"/cart/{self.user_id}",
            name="[READ] View Cart",
            catch_response=True
        ) as response:
            if response.status_code == 200:
                try:
                    data = response.json()
                    if "user_id" in data:
                        response.success()
                    else:
                        response.failure("Invalid cart data")
                except Exception as e:
                    response.failure(f"Failed to parse cart: {e}")
            else:
                response.failure(f"Cart view failed: {response.status_code}")
    
    @task(5)  # Weight: 5 (occasional add to cart - WRITE)
    def add_to_cart(self):
        """Add item to cart - WRITE OPERATION (minority)"""
        if not self.viewed_products:
            return  # Skip if no products viewed yet
        
        product_id = random.choice(self.viewed_products)
        quantity = random.randint(1, 3)
        
        with self.client.post(
            f"/cart/{self.user_id}/items",
            json={"product_id": product_id, "quantity": quantity},
            name="[WRITE] Add to Cart",
            catch_response=True
        ) as response:
            if response.status_code == 200:
                try:
                    data = response.json()
                    if "items" in data:
                        response.success()
                    else:
                        response.failure("Invalid cart response")
                except Exception as e:
                    response.failure(f"Failed to parse cart: {e}")
            else:
                response.failure(f"Add to cart failed: {response.status_code}")
    
    @task(3)  # Weight: 3 (update cart quantity - WRITE)
    def update_cart_quantity(self):
        """Update item quantity in cart - WRITE OPERATION (minority)"""
        if not self.viewed_products:
            return
        
        product_id = random.choice(self.viewed_products)
        quantity = random.randint(1, 5)
        
        with self.client.put(
            f"/cart/{self.user_id}/items/{product_id}",
            json={"quantity": quantity},
            name="[WRITE] Update Cart",
            catch_response=True
        ) as response:
            if response.status_code in [200, 404]:
                response.success()  # 404 is ok if item not in cart
            else:
                response.failure(f"Update failed: {response.status_code}")
    
    @task(2)  # Weight: 2 (rare checkout - WRITE)
    def checkout(self):
        """Complete checkout - WRITE OPERATION (rare)"""
        with self.client.post(
            f"/cart/{self.user_id}/checkout",
            name="[WRITE] Checkout",
            catch_response=True
        ) as response:
            if response.status_code in [201, 400]:
                response.success()  # 400 is ok if cart is empty
            else:
                response.failure(f"Checkout failed: {response.status_code}")
    
    @task(5)  # Weight: 5 (check health)
    def check_health(self):
        """Health check - monitoring"""
        with self.client.get(
            "/health",
            name="[MONITOR] Health",
            catch_response=True
        ) as response:
            if response.status_code in [200, 503]:
                response.success()
            else:
                response.failure(f"Health check failed: {response.status_code}")
    
    @task(5)  # Weight: 5 (check memory stats)
    def check_memory(self):
        """Check memory and operation stats - monitoring"""
        with self.client.get(
            "/memory",
            name="[MONITOR] Memory Stats",
            catch_response=True
        ) as response:
            if response.status_code == 200:
                try:
                    data = response.json()
                    # Log read/write ratio periodically
                    reads = data.get("read_operations", 0)
                    writes = data.get("write_operations", 0)
                    if reads > 0 and reads % 100 == 0:
                        ratio = reads / max(writes, 1)
                        print(f"\n📊 Operations - Reads: {reads}, Writes: {writes}, Ratio: {ratio:.1f}:1")
                    response.success()
                except Exception as e:
                    response.failure(f"Failed to parse memory stats: {e}")
            else:
                response.failure(f"Memory stats failed: {response.status_code}")

