"""
WRITE-HEAVY Workload Test for E-Commerce API
Simulates active shopping behavior: frequent cart updates and checkouts
Ratio: 30% reads, 70% writes
Tests DynamoDB write capacity and potential bottlenecks
"""

from locust import HttpUser, task, between, events
import random


@events.test_start.add_listener
def on_test_start(environment, **kwargs):
    """Called when the test starts"""
    print("\n" + "="*60)
    print("✍️  WRITE-HEAVY WORKLOAD TEST")
    print("="*60)
    print("Simulating active shopping behavior (30% reads, 70% writes)")
    print("Focus: Testing write capacity, transaction throughput")
    print("="*60 + "\n")


class ActiveShopperUser(HttpUser):
    """Simulates users actively shopping (write-heavy behavior)"""
    
    # Faster pace - active shoppers make quick decisions
    wait_time = between(0.5, 2)
    
    # Search terms for quick product finding
    search_terms = [
        "electronics", "books", "home", "sports",
        "alpha", "beta", "gamma", "delta", 
        "fashion", "toys", "garden", "automotive"
    ]
    
    def on_start(self):
        """Initialize user session"""
        # Each user gets a unique ID
        self.user_id = f"shopper_{random.randint(1000, 9999)}"
        self.cart_products = []
        self.order_count = 0
    
    @task(10)  # Weight: 10 (quick product search)
    def quick_search(self):
        """Quick product search - READ OPERATION"""
        query = random.choice(self.search_terms)
        
        with self.client.get(
            f"/products/search?q={query}",
            name="[READ] Quick Search",
            catch_response=True
        ) as response:
            if response.status_code == 200:
                try:
                    data = response.json()
                    if "products" in data and len(data["products"]) > 0:
                        # Immediately grab product IDs for adding to cart
                        for product in data["products"][:5]:
                            if product["id"] not in self.cart_products:
                                self.cart_products.append(product["id"])
                        response.success()
                    else:
                        response.success()
                except Exception as e:
                    response.failure(f"Search failed: {e}")
            else:
                response.failure(f"Status: {response.status_code}")
    
    @task(5)  # Weight: 5 (view specific product)
    def view_product(self):
        """View product details - READ OPERATION"""
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
                        self.cart_products.append(data["id"])
                        response.success()
                    else:
                        response.failure("Invalid product")
                except Exception as e:
                    response.failure(f"Parse error: {e}")
            elif response.status_code == 404:
                response.success()
            else:
                response.failure(f"Status: {response.status_code}")
    
    @task(30)  # Weight: 30 (frequent add to cart - WRITE)
    def add_to_cart(self):
        """Add items to cart frequently - PRIMARY WRITE OPERATION"""
        # Use known products or random ones
        if self.cart_products and random.random() > 0.3:
            product_id = random.choice(self.cart_products)
        else:
            product_id = random.randint(1, 100000)
        
        quantity = random.randint(1, 5)
        
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
                    response.failure(f"Parse error: {e}")
            else:
                response.failure(f"Add failed: {response.status_code}")
    
    @task(25)  # Weight: 25 (frequent cart updates - WRITE)
    def update_cart_items(self):
        """Update cart item quantities - WRITE OPERATION"""
        if not self.cart_products:
            return
        
        product_id = random.choice(self.cart_products)
        
        # 20% chance to remove item (quantity = 0)
        if random.random() < 0.2:
            quantity = 0
        else:
            quantity = random.randint(1, 10)
        
        with self.client.put(
            f"/cart/{self.user_id}/items/{product_id}",
            json={"quantity": quantity},
            name="[WRITE] Update Cart Item",
            catch_response=True
        ) as response:
            if response.status_code in [200, 404]:
                response.success()
            else:
                response.failure(f"Update failed: {response.status_code}")
    
    @task(5)  # Weight: 5 (view cart before checkout)
    def view_cart_before_checkout(self):
        """Check cart contents - READ OPERATION"""
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
                        response.failure("Invalid cart")
                except Exception as e:
                    response.failure(f"Parse error: {e}")
            else:
                response.failure(f"Cart view failed: {response.status_code}")
    
    @task(20)  # Weight: 20 (frequent checkouts - WRITE)
    def checkout_order(self):
        """Complete checkout - HEAVY WRITE OPERATION"""
        with self.client.post(
            f"/cart/{self.user_id}/checkout",
            name="[WRITE] Checkout",
            catch_response=True
        ) as response:
            if response.status_code == 201:
                try:
                    data = response.json()
                    if "order_id" in data:
                        self.order_count += 1
                        # Log successful checkouts periodically
                        if self.order_count % 5 == 0:
                            print(f"✅ User {self.user_id}: {self.order_count} orders completed")
                        response.success()
                    else:
                        response.failure("Invalid order response")
                except Exception as e:
                    response.failure(f"Parse error: {e}")
            elif response.status_code == 400:
                response.success()  # Empty cart is acceptable
            else:
                response.failure(f"Checkout failed: {response.status_code}")
    
    @task(3)  # Weight: 3 (check order status)
    def view_order(self):
        """View order details - READ OPERATION"""
        # Generate a plausible order ID (may not exist)
        order_id = f"ORD-{self.user_id}-{random.randint(1000000000, 9999999999)}"
        
        with self.client.get(
            f"/orders/{order_id}",
            name="[READ] View Order",
            catch_response=True
        ) as response:
            if response.status_code in [200, 404]:
                response.success()  # Both are valid responses
            else:
                response.failure(f"Order view failed: {response.status_code}")
    
    @task(2)  # Weight: 2 (monitor health)
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
                response.failure(f"Health failed: {response.status_code}")
    
    @task(2)  # Weight: 2 (monitor operations)
    def check_operations(self):
        """Check operation statistics - monitoring"""
        with self.client.get(
            "/memory",
            name="[MONITOR] Operations Stats",
            catch_response=True
        ) as response:
            if response.status_code == 200:
                try:
                    data = response.json()
                    # Log write/read ratio periodically
                    reads = data.get("read_operations", 0)
                    writes = data.get("write_operations", 0)
                    if writes > 0 and writes % 100 == 0:
                        ratio = writes / max(reads, 1)
                        print(f"\n📊 Operations - Writes: {writes}, Reads: {reads}, Ratio: {ratio:.1f}:1")
                    response.success()
                except Exception as e:
                    response.failure(f"Parse error: {e}")
            else:
                response.failure(f"Stats failed: {response.status_code}")

