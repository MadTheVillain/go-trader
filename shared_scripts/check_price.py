#!/usr/bin/env python3
"""
Quick price fetcher for the Go scheduler.
Fetches current prices for given symbols.

Usage: python3 check_price.py BTC ETH SOL NQ
"""

import sys
import json
import traceback

SYMBOL_MAP = {
    "BTC": "BTC/USDT",
    "ETH": "ETH/USDT",
    "SOL": "SOL/USDT",
    "BNB": "BNB/USDT",
    "NQ": "NQ",
}


def fetch_price_yahoo(symbol):
    import yfinance
    ticker = yfinance.Ticker(symbol)
    data = ticker.fast_info
    return data.last_price


def fetch_price_ccxt(symbol):
    import ccxt
    ex = ccxt.binanceus({"enableRateLimit": True})
    ticker = ex.fetch_ticker(symbol)
    return ticker["last"]


def main():
    symbols = sys.argv[1:]
    if not symbols:
        print(json.dumps({}))
        return

    prices = {}
    for raw in symbols:
        sym = SYMBOL_MAP.get(raw, raw)
        try:
            if sym == "NQ":
                # NQ futures via yfinance
                price = fetch_price_yahoo("NQ=F")
            else:
                # Crypto via ccxt
                price = fetch_price_ccxt(sym)
            prices[raw] = round(price, 2)
        except Exception as e:
            print(f"Failed to fetch {raw}: {e}", file=sys.stderr)

    print(json.dumps(prices))


if __name__ == "__main__":
    main()
