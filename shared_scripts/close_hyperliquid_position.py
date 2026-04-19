#!/usr/bin/env python3
"""
Hyperliquid emergency position close script (issue #341).

Submits a reduce-only market close for a single coin via the HL SDK's
`market_close`. Used by the portfolio kill switch in the Go scheduler to
liquidate on-chain exposure regardless of which strategy "owns" the
position — including shared coins where per-strategy reconciliation
deliberately leaves virtual state empty.

Usage:
    close_hyperliquid_position.py --symbol=ETH --mode=live

Live mode is required (kill switch is meaningful only against real
positions). Stdout is a single JSON envelope; exit 1 on error so the Go
caller can latch the kill switch and retry next cycle.
"""

import argparse
import json
import os
import sys
import traceback
from datetime import datetime, timezone


sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "platforms", "hyperliquid"))


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--symbol", required=True)
    parser.add_argument("--mode", default="live")
    args = parser.parse_args()

    if args.mode != "live":
        print(json.dumps({
            "close": None,
            "platform": "hyperliquid",
            "timestamp": datetime.now(timezone.utc).isoformat(),
            "error": "--mode=live required for emergency close",
        }))
        sys.exit(1)

    try:
        from adapter import HyperliquidExchangeAdapter
        adapter = HyperliquidExchangeAdapter()
        result = adapter.market_close(args.symbol)

        # SDK reduce-only close response shape mirrors market_open:
        # {"status": "ok", "response": {"type": "order", "data": {"statuses": [...]}}}
        # When already flat the SDK returns either an empty statuses list or
        # status="ok" with no fill block — both are treated as success.
        fill = {}
        try:
            statuses = (
                (result or {}).get("response", {}).get("data", {}).get("statuses", [])
            )
            if statuses:
                filled = statuses[0].get("filled", {})
                fill = {
                    "avg_px": float(filled.get("avgPx", 0) or 0),
                    "total_sz": float(filled.get("totalSz", 0) or 0),
                }
                oid = filled.get("oid")
                if oid is not None:
                    fill["oid"] = int(oid)
        except Exception:
            pass

        # Surface SDK-level error status so the Go caller can latch.
        if isinstance(result, dict) and result.get("status") not in (None, "ok"):
            print(json.dumps({
                "close": {"symbol": args.symbol, "fill": fill},
                "platform": "hyperliquid",
                "timestamp": datetime.now(timezone.utc).isoformat(),
                "error": f"sdk status={result.get('status')!r}: {result}",
            }))
            sys.exit(1)

        print(json.dumps({
            "close": {"symbol": args.symbol, "fill": fill},
            "platform": "hyperliquid",
            "timestamp": datetime.now(timezone.utc).isoformat(),
        }))
    except Exception as e:
        traceback.print_exc(file=sys.stderr)
        print(json.dumps({
            "close": None,
            "platform": "hyperliquid",
            "timestamp": datetime.now(timezone.utc).isoformat(),
            "error": str(e),
        }))
        sys.exit(1)


if __name__ == "__main__":
    main()
