from fastapi import FastAPI, HTTPException, Query
from pydantic import BaseModel
from typing import Optional, Tuple
from cachetools import TTLCache
from playwright.sync_api import sync_playwright
import re, time, json

app = FastAPI(title="Alsuper Scraper", version="4.3")

# Cache 5 minutos
cache = TTLCache(maxsize=128, ttl=300)

class PriceResponse(BaseModel):
    url: str
    product_name: Optional[str] = None
    price_per_kg: Optional[float] = None       # MXN/kg (carnes/verduras)
    unit_price: Optional[float] = None         # MXN por unidad/paquete/six-pack/lata
    unit_pack_size: Optional[int] = None       # p.ej. 6 para six-pack
    unit_weight_g: Optional[int] = None        # p.ej. chorizo pieza=100g, salchichas paquete=800g
    currency: str = "MXN"
    raw_unit: Optional[str] = None             # "kg", "pieza", "paquete", "lata", "six-pack", etc.

def _num(txt: str) -> Optional[float]:
    if not txt:
        return None
    clean = txt.replace(",", "").replace("$", "").strip()
    m = re.search(r"(\d+(?:\.\d{1,2})?)", clean)
    return float(m.group(1)) if m else None

def _guess_units(url: str, page_text: str) -> Tuple[str, Optional[int], Optional[int]]:
    """
    Unidad + pack y peso por pieza/paquete.
    """
    low = (url + " " + page_text).lower()

    # Cerveza: six-pack (precio del paquete completo)
    if "cerveza-six-pack" in low or "six pack" in low or "six-pack" in low:
        return "six-pack", 6, None

    # Jugos/latas individuales
    if "nectar-mixto" in low or "v8" in low or "lata" in low:
        return "lata", 1, None

    # Carnes/verduras (por kg)
    if "kg" in low or "kilo" in low or any(k in low for k in ["pulpa-de-res", "jamon-de-pierna", "tocineta", "cebolla"]):
        return "kg", None, None

    # Chorizo por pieza (100g)
    if "chorizo" in low:
        return "pieza", 1, 100

    # Salchichas por paquete (800g)
    if "salchicha" in low or "salchichas" in low:
        return "paquete", 1, 800

    # Si el propio texto menciona "paquete"
    if "paquete" in low:
        return "paquete", 1, 800

    # Fallback
    return "pieza", 1, None

def _close_banners(page):
    for sel in [
        'button:has-text("Aceptar")','button:has-text("ACEPTAR")',
        'button:has-text("Aceptar cookies")','button:has-text("Entendido")',
        '[id*="cookie"] button','[class*="cookie"] button',
        'button:has-text("Cerrar")','[aria-label="Cerrar"]',
        'button:has-text("Permitir")','button:has-text("Aceptar todo")',
        'button:has-text("Seleccionar tienda")','button:has-text("Continuar")',
    ]:
        try:
            page.locator(sel).first.click(timeout=1200)
            time.sleep(0.1)
        except Exception:
            pass

# Selector list con clases típicas que hemos visto en Alsuper
# Priorizamos mat-label + clases de precio/color
PRICE_SELECTORS = [
    # Descuento/normal (rojo)
    "mat-label.as-discount-price.as-font-red-blood",
    "mat-label.as-price.as-font-red-blood",
    # Variantes equivalentes
    "[class*='as-discount-price'][class*='as-font-red']",
    "[class*='as-price'][class*='as-font-red']",
    # Negro
    "mat-label.as-price",
    "[class*='as-price']",
    # Fallbacks muy cercanos al h1 (si cambian la etiqueta pero conservan clases)
    "h1 + * [class*='as-price']",
    "h1 ~ * [class*='as-price']",
]

# Colores aceptados (computados)
def _color_is_red_or_black(color_str: str) -> Optional[str]:
    s = (color_str or "").lower().replace(" ", "")
    # rgb / rgba
    if s.startswith("rgb"):
        try:
            nums = s[s.index("(")+1:s.index(")")].split(",")
            r = int(float(nums[0])); g = int(float(nums[1])); b = int(float(nums[2]))
            # negro “casi negro”
            if r <= 10 and g <= 10 and b <= 10:
                return "black"
            # rojo intenso aproximado
            if r >= 180 and g <= 60 and b <= 60:
                return "red"
        except Exception:
            return None
    # hex
    if s in ("#000", "#000000"):
        return "black"
    if s in ("#f00", "#ff0000"):
        return "red"
    # keywords
    if "black" in s: return "black"
    if "red" in s: return "red"
    return None

FIND_PRICE_JS = r"""
(selList) => {
  function isStruck(el) {
    const cs = getComputedStyle(el);
    const td = (cs.textDecoration || cs.textDecorationLine || '').toLowerCase();
    return td.includes('line-through');
  }
  function hasDollarDigits(text) {
    if (!text) return false;
    return /\$\s*\d/.test(text);
  }
  const out = [];
  for (const sel of selList) {
    const nodes = Array.from(document.querySelectorAll(sel));
    for (const el of nodes) {
      if (!el) continue;
      const txt = (el.textContent || "").trim();
      if (!hasDollarDigits(txt)) continue; // $ y dígitos en el MISMO nodo
      const cs = getComputedStyle(el);
      const struck = isStruck(el);
      out.push({
        selector: sel,
        text: txt,
        color: cs.color,
        struck: struck
      });
    }
    if (out.length) break; // si ya hay con este selector, no seguimos a los demás (prioridad)
  }
  return out;
}
"""

@app.get("/price", response_model=PriceResponse)
def get_price(url: str, debug: int = Query(default=0, ge=0, le=1)):
    if url in cache:
        return cache[url]

    with sync_playwright() as p:
        browser = p.chromium.launch(args=["--no-sandbox"])
        context = browser.new_context(
            locale="es-MX",
            user_agent="Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/118 Safari/537.36",
            viewport={"width": 1280, "height": 900},
        )
        page = context.new_page()

        try:
            page.goto(url, timeout=120000)
            page.wait_for_load_state("networkidle")
            page.evaluate("window.scrollTo(0,0)")
            _close_banners(page)
            page.locator("h1").first.wait_for(timeout=20000)
        except Exception as e:
            browser.close()
            raise HTTPException(status_code=502, detail=f"No se pudo cargar {url}: {e}")

        # Nombre del producto
        try:
            product_name = page.locator("h1").first.inner_text(timeout=2000).strip()
        except Exception:
            product_name = None

        # Buscar precio usando los selectores específicos y filtros:
        # - ($ + dígitos) en el mismo nodo
        # - NO tachado
        # - color rojo o negro
        price_val = None
        picked = None
        raw_list = []
        deadline = time.time() + 18.0
        while time.time() < deadline and price_val is None:
            try:
                raw_list = page.evaluate(FIND_PRICE_JS, PRICE_SELECTORS)
                # Filtrar por no tachados y color válido
                filtered = []
                for it in raw_list:
                    if it.get("struck"):
                        continue
                    colorType = _color_is_red_or_black(it.get("color",""))
                    if not colorType:
                        continue
                    val = _num(it.get("text",""))
                    if val:
                        it["value"] = val
                        it["colorType"] = colorType
                        filtered.append(it)
                # Prioridad: primero rojo, luego negro. Si múltiples, el primero.
                red_first = [x for x in filtered if x["colorType"] == "red"]
                black_next = [x for x in filtered if x["colorType"] == "black"]
                pick = (red_first[0] if red_first else (black_next[0] if black_next else None))
                if pick:
                    price_val = pick["value"]
                    picked = pick
                    break
            except Exception:
                pass
            page.wait_for_timeout(350)

        if debug:
            print("=== DEBUG SCRAPER ===")
            print("URL:", url)
            print("H1:", product_name)
            print("CANDIDATOS (raw, top 5):")
            for c in raw_list[:5]:
                print(" -", json.dumps(c, ensure_ascii=False))
            print("PICK:", json.dumps(picked, ensure_ascii=False) if picked else None)

        if price_val is None:
            browser.close()
            raise HTTPException(status_code=500, detail="No se encontró precio visible no tachado con '$' (rojo/negro) usando selectores de Alsuper.")

        # Texto general (para heurística de unidad)
        try:
            page_text = page.locator("body").inner_text(timeout=2000)
        except Exception:
            page_text = ""

        unit, pack_size, unit_weight_g = _guess_units(url, page_text)

        price_per_kg = None
        unit_price = None
        raw_unit = unit

        if unit == "kg":
            price_per_kg = price_val
        else:
            unit_price = price_val

        browser.close()

    resp = PriceResponse(
        url=url,
        product_name=product_name,
        price_per_kg=price_per_kg,
        unit_price=unit_price,
        unit_pack_size=pack_size,
        unit_weight_g=unit_weight_g,
        currency="MXN",
        raw_unit=raw_unit,
    )
    cache[url] = resp
    return resp
