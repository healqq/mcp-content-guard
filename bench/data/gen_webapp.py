#!/usr/bin/env python3
"""Generate bench/data/webapp/index.html with 300-row orders table."""

import os
from datetime import datetime, timedelta

SCRIPT_DIR = os.path.dirname(os.path.abspath(__file__))
OUT_PATH = os.path.join(SCRIPT_DIR, "webapp", "index.html")

COMPANIES = ["Acme Corp","GlobalTech Inc","MegaRetail","FastShop Ltd","TopBuyer Co","BestClient","QuickOrder","PrimePurchase","AlphaTrade","ZetaMarket"]
REGIONS = ["EMEA","APAC","AMER","LATAM"]
STATUSES = ["Processing","Shipped","Delivered","Cancelled","Pending"]

html_rows = []
for i in range(1, 301):
    company = COMPANIES[(i-1) % 10]
    email = company.lower().split()[0] + f"{i:03d}@business.com"
    region = REGIONS[(i-1) % 4]
    status = STATUSES[(i-1) % 5]
    amount = f"${((i * 11) % 5000 + 100):.2f}"
    date_str = (datetime(2024, 1, 1) + timedelta(days=(i-1))).strftime('%Y-%m-%d')
    items = (i % 10) + 1
    order_id = f"ORD-{i:04d}"
    html_rows.append(
        f"<tr><td>{order_id}</td><td>{company}</td><td>{email}</td>"
        f"<td>{region}</td><td>{status}</td><td>{amount}</td>"
        f"<td>{date_str}</td><td>{items}</td></tr>"
    )

rows_html = "\n".join(html_rows)

html = f"""<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Order Management Dashboard</title>
<style>
  body {{ font-family: Arial, sans-serif; margin: 20px; background: #f5f5f5; }}
  h1 {{ color: #333; margin-bottom: 20px; }}
  table {{ border-collapse: collapse; width: 100%; background: #fff; box-shadow: 0 1px 3px rgba(0,0,0,0.2); }}
  th {{ background: #4a90e2; color: white; padding: 10px 12px; text-align: left; font-weight: 600; }}
  td {{ padding: 8px 12px; border-bottom: 1px solid #e0e0e0; }}
  tr:hover {{ background: #f0f7ff; }}
  tr:last-child td {{ border-bottom: none; }}
</style>
</head>
<body>
<h1>Order Management Dashboard</h1>
<table id="orders-table">
<thead>
<tr>
  <th>Order ID</th>
  <th>Company</th>
  <th>Contact Email</th>
  <th>Region</th>
  <th>Status</th>
  <th>Amount</th>
  <th>Date</th>
  <th>Items</th>
</tr>
</thead>
<tbody>
{rows_html}
</tbody>
</table>
</body>
</html>
"""

with open(OUT_PATH, 'w', encoding='utf-8') as f:
    f.write(html)

print(f"Written index.html with {len(html_rows)} rows")
print(f"ORD-0182: status={STATUSES[(182-1)%5]}, region={REGIONS[(182-1)%4]}")
emea_count = sum(1 for i in range(1, 301) if REGIONS[(i-1)%4] == "EMEA")
print(f"EMEA count: {emea_count}")
