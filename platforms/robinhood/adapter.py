"""
Robinhood Crypto Exchange Adapter.

Supports paper (signal-only using yfinance for OHLCV, no credentials needed) and
live (real orders via robin_stocks, credentials required) modes.

Environment variables:
    ROBINHOOD_USERNAME     — Robinhood account email/username
    ROBINHOOD_PASSWORD     — Robinhood account password
    ROBINHOOD_TOTP_SECRET  — TOTP secret for MFA (base32 string from authenticator setup)
"""

import os
import sys

# Yahoo Finance crypto symbol mapping (paper mode OHLCV + fallback prices)
YAHOO_CRYPTO_MAP = {
    "BTC": "BTC-USD",
    "ETH": "ETH-USD",
    "SOL": "SOL-USD",
    "DOGE": "DOGE-USD",
    "AVAX": "AVAX-USD",
    "LINK": "LINK-USD",
    "ADA": "ADA-USD",
    "DOT": "DOT-USD",
    "MATIC": "MATIC-USD",
    "SHIB": "SHIB-USD",
}


class RobinhoodExchangeAdapter:
    """
    Exchange adapter for Robinhood crypto trading.

    Paper mode:  no credentials needed; uses yfinance for OHLCV and price data.
    Live mode:   requires ROBINHOOD_USERNAME, ROBINHOOD_PASSWORD, ROBINHOOD_TOTP_SECRET;
                 uses robin_stocks for live prices and order execution.
    """

    def __init__(self, mode="paper"):
        self._mode = mode
        self._logged_in = False

        if mode == "live":
            self._login()
        else:
            # Paper mode: attempt login for live prices, but don't fail if missing
            try:
                self._login()
            except Exception:
                pass

    def _login(self):
        """Authenticate with Robinhood via robin_stocks + TOTP."""
        username = os.environ.get("ROBINHOOD_USERNAME", "")
        password = os.environ.get("ROBINHOOD_PASSWORD", "")
        totp_secret = os.environ.get("ROBINHOOD_TOTP_SECRET", "")

        if not username or not password or not totp_secret:
            if self._mode == "live":
                raise RuntimeError(
                    "Live mode requires ROBINHOOD_USERNAME, ROBINHOOD_PASSWORD, "
                    "and ROBINHOOD_TOTP_SECRET environment variables"
                )
            return

        import robin_stocks.robinhood as rh
        import pyotp

        totp = pyotp.TOTP(totp_secret).now()
        rh.login(username, password, mfa_code=totp)
        self._logged_in = True

    @property
    def is_live(self) -> bool:
        return self._mode == "live" and self._logged_in

    @property
    def mode(self) -> str:
        return self._mode

    @property
    def name(self) -> str:
        return "robinhood"

    # ─────────────────────────────────────────────
    # Market data
    # ─────────────────────────────────────────────

    def get_price(self, symbol: str) -> float:
        """Get current crypto price. Uses robin_stocks if logged in, else yfinance."""
        if self._logged_in:
            try:
                import robin_stocks.robinhood as rh
                quote = rh.crypto.get_crypto_quote(symbol)
                if quote and quote.get("mark_price"):
                    return float(quote["mark_price"])
            except Exception as e:
                print(f"[robinhood] robin_stocks price error for {symbol}: {e}", file=sys.stderr)
        return self._get_yahoo_price(symbol)

    def get_spot_price(self, symbol: str) -> float:
        """Alias for get_price (protocol compatibility)."""
        return self.get_price(symbol)

    def get_ohlcv(self, symbol: str, interval: str = "1h", limit: int = 200) -> list:
        """
        Fetch OHLCV candles via yfinance (robin_stocks has no OHLCV endpoint).

        Returns list of [timestamp_ms, open, high, low, close, volume].
        """
        return self._get_yahoo_ohlcv(symbol, interval, limit)

    # ─────────────────────────────────────────────
    # Yahoo Finance helpers
    # ─────────────────────────────────────────────

    def _get_yahoo_price(self, symbol: str) -> float:
        """Fetch current price via yfinance."""
        yahoo_sym = YAHOO_CRYPTO_MAP.get(symbol)
        if not yahoo_sym:
            return 0.0
        try:
            import yfinance as yf
            ticker = yf.Ticker(yahoo_sym)
            hist = ticker.history(period="1d")
            if hist.empty:
                return 0.0
            return float(hist["Close"].iloc[-1])
        except ImportError:
            print("[robinhood] yfinance not installed. Run: uv add yfinance", file=sys.stderr)
            return 0.0
        except Exception as e:
            print(f"[robinhood] yahoo price error for {symbol}: {e}", file=sys.stderr)
            return 0.0

    def _get_yahoo_ohlcv(self, symbol: str, interval: str = "1h", limit: int = 200) -> list:
        """Fetch OHLCV via yfinance for crypto symbols."""
        yahoo_sym = YAHOO_CRYPTO_MAP.get(symbol)
        if not yahoo_sym:
            return []
        try:
            import yfinance as yf
            yf_interval = interval
            if "m" in interval:
                period = "5d"
            elif interval in ("1h", "60m"):
                period = "30d"
            else:
                period = "1y"
            ticker = yf.Ticker(yahoo_sym)
            hist = ticker.history(period=period, interval=yf_interval)
            if hist.empty:
                return []
            result = []
            for idx, row in hist.iterrows():
                ts_ms = int(idx.timestamp() * 1000)
                result.append([
                    ts_ms,
                    float(row["Open"]),
                    float(row["High"]),
                    float(row["Low"]),
                    float(row["Close"]),
                    float(row.get("Volume", 0)),
                ])
            return result[-limit:]
        except ImportError:
            print("[robinhood] yfinance not installed. Run: uv add yfinance", file=sys.stderr)
            return []
        except Exception as e:
            print(f"[robinhood] yahoo ohlcv error for {symbol}: {e}", file=sys.stderr)
            return []

    # ─────────────────────────────────────────────
    # Order execution (live mode only)
    # ─────────────────────────────────────────────

    def market_buy(self, symbol: str, amount_usd: float) -> dict:
        """
        Buy crypto by USD amount. Live mode only.
        Returns robin_stocks order response dict.
        """
        if not self.is_live:
            raise RuntimeError("market_buy requires live mode")
        import robin_stocks.robinhood as rh
        result = rh.orders.order_buy_crypto_by_price(symbol, amount_usd)
        return result or {}

    def market_sell(self, symbol: str, quantity: float) -> dict:
        """
        Sell crypto by quantity. Live mode only.
        Returns robin_stocks order response dict.
        """
        if not self.is_live:
            raise RuntimeError("market_sell requires live mode")
        import robin_stocks.robinhood as rh
        result = rh.orders.order_sell_crypto_by_quantity(symbol, quantity)
        return result or {}

    def get_crypto_positions(self) -> list:
        """Get current crypto positions from Robinhood."""
        if not self._logged_in:
            return []
        try:
            import robin_stocks.robinhood as rh
            positions = rh.crypto.get_crypto_positions()
            result = []
            for pos in positions:
                qty = float(pos.get("quantity", 0) or 0)
                if qty <= 0:
                    continue
                currency = pos.get("currency", {})
                symbol = currency.get("code", "")
                cost_basis = float(pos.get("cost_bases", [{}])[0].get("direct_cost_basis", 0) or 0)
                avg_price = cost_basis / qty if qty > 0 else 0
                result.append({
                    "symbol": symbol,
                    "quantity": qty,
                    "avg_price": avg_price,
                })
            return result
        except Exception as e:
            print(f"[robinhood] get_crypto_positions error: {e}", file=sys.stderr)
            return []
