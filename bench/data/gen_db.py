#!/usr/bin/env python3
"""Generate bench/data/bench.db with deterministic test data."""

import sqlite3
import os
from datetime import date, timedelta

SCRIPT_DIR = os.path.dirname(os.path.abspath(__file__))
DB_PATH = os.path.join(SCRIPT_DIR, "bench.db")

COUNTRIES = ["US", "DE", "GB", "FR", "JP", "CA", "AU", "BR", "IN", "MX"]
CATEGORIES = ["Electronics", "Clothing", "Books", "Food", "Tools"]
STATUSES = ["pending", "processing", "shipped", "delivered", "cancelled"]

conn = sqlite3.connect(DB_PATH)
cur = conn.cursor()

cur.executescript("""
DROP TABLE IF EXISTS orders;
DROP TABLE IF EXISTS products;
DROP TABLE IF EXISTS customers;

CREATE TABLE customers (
    id      INTEGER PRIMARY KEY,
    name    TEXT NOT NULL,
    email   TEXT NOT NULL,
    country TEXT NOT NULL
);

CREATE TABLE products (
    id       INTEGER PRIMARY KEY,
    name     TEXT NOT NULL,
    category TEXT NOT NULL,
    price    REAL NOT NULL,
    stock    INTEGER NOT NULL
);

CREATE TABLE orders (
    id          INTEGER PRIMARY KEY,
    customer_id INTEGER NOT NULL,
    product_id  INTEGER NOT NULL,
    status      TEXT NOT NULL,
    amount      REAL NOT NULL,
    created_at  TEXT NOT NULL
);
""")

# customers: 200 rows
customers = []
for i in range(1, 201):
    name = f"Customer {i:03d}"
    email = f"customer{i:03d}@example.com"
    country = COUNTRIES[i % 10]
    customers.append((i, name, email, country))
cur.executemany("INSERT INTO customers VALUES (?,?,?,?)", customers)

# products: 100 rows
products = []
for i in range(1, 101):
    category = CATEGORIES[i % 5]
    price = round(((i * 7) % 199) + 1.99, 2)
    stock = (i * 3) % 97
    name = f"{category} Item {i:03d}"
    products.append((i, name, category, price, stock))
cur.executemany("INSERT INTO products VALUES (?,?,?,?,?)", products)

# orders: 500 rows
base_date = date(2024, 1, 1)
orders = []
for i in range(1, 501):
    customer_id = (i % 200) + 1
    product_id = (i % 100) + 1
    status = STATUSES[i % 5]
    amount = round(((i * 11) % 500) + 9.99, 2)
    created_at = (base_date + timedelta(days=i)).isoformat()
    orders.append((i, customer_id, product_id, status, amount, created_at))
cur.executemany("INSERT INTO orders VALUES (?,?,?,?,?,?)", orders)

conn.commit()

# Compute summary values
cur.execute("SELECT email FROM customers WHERE id=42")
cust42_email = cur.fetchone()[0]

cur.execute("SELECT price FROM products WHERE id=73")
prod73_price = cur.fetchone()[0]

cur.execute("SELECT COUNT(*) FROM orders WHERE status='pending'")
pending_count = cur.fetchone()[0]

cur.execute("SELECT COUNT(*) FROM orders WHERE status='shipped'")
shipped_count = cur.fetchone()[0]

conn.close()

print("Generated bench.db:")
print(f"  customers: 200 rows  (example: {cust42_email} for id=42)")
print(f"  products: 100 rows   (example: product id=73, price={prod73_price})")
print(f"  orders: 500 rows     (pending count={pending_count}, shipped count={shipped_count})")
