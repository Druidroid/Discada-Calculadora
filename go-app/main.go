package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"math"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// En producción (Render) apuntamos al scraper público HTTPS
const scraperBase = "https://discada-scraper-1.onrender.com/price?url="

// Timeout total por request al scraper (cada ingrediente)
const perReqTimeout = 60 * time.Second

// Cache TTL de 5 minutos
const cacheTTL = 5 * time.Minute

// Cliente HTTP con timeout y TLS relajado (para evitar error x509 en Render)
var httpClient = &http.Client{
	Timeout: perReqTimeout,
	Transport: &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true, // ⚠️ Acepta el certificado del scraper sin validarlo
		},
	},
}

// -------------------- Datos de receta --------------------

var (
	// Total receta base (solo se usa para escalar bebidas)
	totalBaseGrams float64 = 2937.5

	// Ratios SOLO de proteínas (suman 1.0 dentro del bloque de proteínas)
	proteinRatios = map[string]float64{
		"Pulpa de res picada": 0.55,
		"Tocino picado":       0.075,
		"Jamon en cuadros":    0.175,
		"Salchicha p/Asar":    0.125,
		"Chorizo":             0.075,
	}

	// Cebolla: ratio separado (no influye en proteínas)
	onionRatio float64 = 0.175

	// Bebidas: cantidades base por tanda
	baseUnits = map[string]float64{
		"Cerveza":             3.125,
		"Jugo de verduras V8": 1.0,
	}

	// URLs de scraping
	ingredientURLs = map[string]string{
		"Pulpa de res picada": "https://alsuper.com/producto/pulpa-de-res-picada-357825",
		"Tocino picado":       "https://alsuper.com/producto/tocineta-413218",
		"Jamon en cuadros":    "https://alsuper.com/producto/jamon-de-pierna-horneado-428669",
		"Salchicha p/Asar":    "https://alsuper.com/producto/salchicha-para-asar-238828",
		"Chorizo":             "https://alsuper.com/producto/chorizo-319544",
		"Cebolla blanca":      "https://alsuper.com/producto/cebolla-blanca-924",
		"Cerveza":             "https://alsuper.com/producto/cerveza-six-pack-lata-323328",
		"Jugo de verduras V8": "https://alsuper.com/producto/nectar-mixto-de-450697",
	}
)

// -------------------- Modelos --------------------

type scraperPrice struct {
	URL           string   `json:"url"`
	ProductName   *string  `json:"product_name,omitempty"`
	PricePerKg    *float64 `json:"price_per_kg,omitempty"`   // para productos a granel O precio mostrado
	UnitPrice     *float64 `json:"unit_price,omitempty"`     // para pieza/paquete/lata/six
	UnitPackSize  *string  `json:"unit_pack_size,omitempty"` // no lo usamos, viene del scraper
	UnitWeightG   *int     `json:"unit_weight_g,omitempty"`  // no lo usamos ahora
	Currency      string   `json:"currency"`
	RawUnit       string   `json:"raw_unit,omitempty"`       // "kg", "pza", etc (solo informativo)
	OriginalPrice *float64 `json:"original_price,omitempty"` // si en algún momento se usa
}

type IngredientCalc struct {
	Name           string  `json:"name"`
	URL            string  `json:"url"`
	GramsNeeded    float64 `json:"grams_needed"`
	UnitsNeeded    int     `json:"units_needed"`    // piezas/latas requeridas (visual)
	PurchasedUnits int     `json:"purchased_units"` // paquetes/piezas/six comprados
	PricePerKg     float64 `json:"price_per_kg"`    // visible en UI si aplica
	UnitPrice      float64 `json:"unit_price"`      // visible en UI si aplica
	Cost           float64 `json:"cost"`
	Currency       string  `json:"currency"`
}

type CalcResponse struct {
	Personas         int              `json:"personas"`
	GramosPorPersona int              `json:"gramos_por_persona"`
	TotalGramos      float64          `json:"total_grams"`
	Items            []IngredientCalc `json:"items"`
	TotalCosto       float64          `json:"total_cost"`
	Currency         string           `json:"currency"`
}

// -------------------- Cache simple --------------------

type priceEntry struct {
	at   time.Time
	data *scraperPrice
}

var (
	priceCache = make(map[string]priceEntry) // key: URL
	cacheMu    sync.RWMutex
)

func cacheGet(url string) (*scraperPrice, bool) {
	cacheMu.RLock()
	ent, ok := priceCache[url]
	cacheMu.RUnlock()
	if !ok {
		return nil, false
	}
	if time.Since(ent.at) > cacheTTL {
		return nil, false
	}
	return ent.data, true
}

func cacheSet(url string, pr *scraperPrice) {
	cacheMu.Lock()
	priceCache[url] = priceEntry{at: time.Now(), data: pr}
	cacheMu.Unlock()
}

// -------------------- Utilidades --------------------

func mustEnv(key, fallback string) string {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	return v
}

func ceilDiv(n, d int) int {
	if n <= 0 {
		return 0
	}
	q := n / d
	if n%d != 0 {
		q++
	}
	return q
}

func round2(x float64) float64 {
	return math.Round(x*100) / 100
}

func urlQueryEscape(s string) string {
	return template.URLQueryEscaper(s)
}

// Llamada base al scraper (usa cache)
func fetchPrice(ctx context.Context, url string) (*scraperPrice, error) {
	if pr, ok := cacheGet(url); ok {
		return pr, nil
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", scraperBase+urlQueryEscape(url), nil)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("llamando scraper: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("scraper status %d", resp.StatusCode)
	}
	var pr scraperPrice
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return nil, fmt.Errorf("decode json: %w", err)
	}
	cacheSet(url, &pr)
	return &pr, nil
}

// Reintentos con backoff
func fetchWithRetry(ctx context.Context, url string, attempts int, baseDelay time.Duration) (*scraperPrice, error) {
	var lastErr error
	for i := 0; i < attempts; i++ {
		attemptCtx, cancel := context.WithTimeout(ctx, perReqTimeout)
		pr, err := fetchPrice(attemptCtx, url)
		cancel()
		if err == nil {
			return pr, nil
		}
		lastErr = err
		time.Sleep(baseDelay * time.Duration(i+1))
	}
	return nil, fmt.Errorf("fetchWithRetry: %w", lastErr)
}

// -------------------- Cálculo --------------------

func calcFor(personas, gpp int) (*CalcResponse, error) {
	if personas <= 0 || gpp <= 0 {
		return nil, fmt.Errorf("personas y gramos por persona deben ser > 0")
	}
	totalGrams := float64(personas * gpp)

	// Cebolla por su propio ratio (no afecta proteínas)
	onionGrams := totalGrams * onionRatio

	// Orden: proteínas + cebolla + bebidas
	names := []string{
		"Pulpa de res picada",
		"Tocino picado",
		"Jamon en cuadros",
		"Salchicha p/Asar",
		"Chorizo",
		"Cebolla blanca",
		"Cerveza",
		"Jugo de verduras V8",
	}

	items := make([]IngredientCalc, len(names))
	errs := make([]error, len(names))

	var wg sync.WaitGroup
	wg.Add(len(names))

	for idx, name := range names {
		go func(i int, nm string) {
			defer wg.Done()

			url := ingredientURLs[nm]
			ctx, cancel := context.WithTimeout(context.Background(), perReqTimeout)
			defer cancel()

			pr, err := fetchWithRetry(ctx, url, 3, 800*time.Millisecond)
			if err != nil {
				errs[i] = fmt.Errorf("%s: %w", nm, err)
				return
			}

			// Precios crudos del scraper
			unitPrice := 0.0
			if pr.UnitPrice != nil {
				unitPrice = *pr.UnitPrice
			}
			pricePerKg := 0.0
			if pr.PricePerKg != nil {
				pricePerKg = *pr.PricePerKg
			}

			it := IngredientCalc{
				Name:       nm,
				URL:        url,
				Currency:   pr.Currency,
				UnitPrice:  unitPrice,
				PricePerKg: pricePerKg,
			}

			switch nm {
			// ---------- Proteínas por KG ----------
			case "Pulpa de res picada", "Tocino picado", "Jamon en cuadros":
				r := proteinRatios[nm]
				gramsNeeded := r * totalGrams
				it.GramsNeeded = gramsNeeded
				kilos := gramsNeeded / 1000.0
				// Si llegó unit_price pero no price_per_kg, úsalo como $/kg
				if it.PricePerKg <= 0 && it.UnitPrice > 0 {
					it.PricePerKg = it.UnitPrice
				}
				it.UnitPrice = 0 // UI: solo mostramos $/kg
				it.Cost = round2(kilos * it.PricePerKg)

			// ---------- Paquetes: Salchicha y Chorizo ----------
			case "Salchicha p/Asar":
				// 800 g por paquete — Costo = Precio Unitario × paquetes
				r := proteinRatios[nm]
				gramsNeeded := r * totalGrams
				it.GramsNeeded = gramsNeeded
				packs := ceilDiv(int(math.Round(gramsNeeded)), 800)
				it.PurchasedUnits = packs

				// Si unit_price viene vacío pero hay price_per_kg,
				// tomar price_per_kg como unit_price (precio del paquete)
				if it.UnitPrice <= 0 && it.PricePerKg > 0 {
					it.UnitPrice = it.PricePerKg
				}
				// Columna Precio Por Kg vacía
				it.PricePerKg = 0
				it.Cost = round2(float64(packs) * it.UnitPrice)

			case "Chorizo":
				// 100 g por paquete — Costo = Precio Unitario × paquetes
				r := proteinRatios[nm]
				gramsNeeded := r * totalGrams
				it.GramsNeeded = gramsNeeded
				packs := ceilDiv(int(math.Round(gramsNeeded)), 100)
				it.PurchasedUnits = packs

				if it.UnitPrice <= 0 && it.PricePerKg > 0 {
					it.UnitPrice = it.PricePerKg
				}
				it.PricePerKg = 0
				it.Cost = round2(float64(packs) * it.UnitPrice)

			// ---------- Cebolla ----------
			case "Cebolla blanca":
				// Por KG, mostrar $/kg, piezas 150g
				it.GramsNeeded = onionGrams
				const onionWeight = 150
				onions := ceilDiv(int(math.Round(onionGrams)), onionWeight)
				it.UnitsNeeded = onions
				if it.PricePerKg <= 0 && it.UnitPrice > 0 {
					it.PricePerKg = it.UnitPrice
				}
				it.UnitPrice = 0
				it.Cost = round2(float64(onions*onionWeight) / 1000.0 * it.PricePerKg)

			// ---------- Bebidas ----------
			case "Cerveza":
				scale := totalGrams / totalBaseGrams
				baseLatas := baseUnits[nm] // 3.125
				latasNecesarias := int(math.Ceil(scale * baseLatas))
				sixPacks := 0
				if latasNecesarias > 0 {
					sixPacks = int(math.Ceil(float64(latasNecesarias) / 6.0))
					if sixPacks < 1 {
						sixPacks = 1
					}
				}
				it.UnitsNeeded = latasNecesarias
				it.PurchasedUnits = sixPacks
				it.PricePerKg = 0
				it.Cost = round2(float64(sixPacks) * it.UnitPrice)

			case "Jugo de verduras V8":
				scale := totalGrams / totalBaseGrams
				baseLatas := baseUnits[nm]
				latas := int(math.Ceil(scale * baseLatas))
				if latas == 0 && scale > 0 {
					latas = 1
				}
				it.UnitsNeeded = latas
				it.PurchasedUnits = latas
				it.PricePerKg = 0
				it.Cost = round2(float64(latas) * it.UnitPrice)
			}

			items[i] = it
		}(idx, name)
	}

	wg.Wait()

	for _, e := range errs {
		if e != nil {
			return nil, e
		}
	}

	var totalCost float64
	currency := "MXN"
	for _, it := range items {
		totalCost += it.Cost
		if it.Currency != "" {
			currency = it.Currency
		}
	}

	out := &CalcResponse{
		Personas:         personas,
		GramosPorPersona: gpp,
		TotalGramos:      round2(totalGrams),
		Items:            items,
		TotalCosto:       round2(totalCost),
		Currency:         currency,
	}
	return out, nil
}

// -------------------- Plantillas HTMX --------------------

// Página principal con HTMX (tema MS-DOS negro/naranja)
const indexPageHTML = `<!doctype html>
<html lang="es">
<head>
  <meta charset="utf-8">
  <title>ccdn.1 Calculadora Discada Norteña</title>
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <script src="https://unpkg.com/htmx.org@1.9.12"></script>
  <style>
    :root {
      --bg: #000000;
      --fg: #ff9800;
      --fg-soft: #ffb74d;
      --accent: #ff9800;
      --accent-soft: #ffb74d;
      --border: #ff9800;
      --table-border: #ff980033;
      --error-bg: #2b0000;
      --error-fg: #ff7043;
    }
    * { box-sizing: border-box; }
    body{
      font-family:system-ui,-apple-system,Segoe UI,Roboto,Ubuntu,Helvetica,Arial,sans-serif;
      max-width:1100px;
      margin:40px auto;
      padding:0 12px;
      background:var(--bg);
      color:var(--fg);
    }
    a{ color:var(--fg-soft); text-decoration:none; }
    a:hover{ text-decoration:underline; }
    h1{ margin-top:0; color:var(--fg-soft); }
    form{
      display:flex;
      gap:12px;
      flex-wrap:wrap;
      align-items:flex-end;
      margin-bottom:16px;
      padding:12px 14px;
      border-radius:10px;
      border:1px solid var(--border);
      background:#050505;
    }
    label{
      display:flex;
      flex-direction:column;
      font-size:14px;
    }
    input[type=number]{
      padding:8px 10px;
      font-size:16px;
      border:1px solid var(--border);
      border-radius:8px;
      min-width:140px;
      background:#000;
      color:var(--fg);
      outline:none;
      caret-color:var(--fg);
    }
    input[type=number]:focus{
      box-shadow:0 0 0 1px var(--accent-soft);
    }
    button{
      padding:10px 16px;
      border:0;
      background:var(--accent);
      color:#000;
      border-radius:10px;
      font-weight:600;
      cursor:pointer;
      font-size:14px;
      letter-spacing:0.03em;
      text-transform:uppercase;
    }
    button:hover{
      background:#ffb74d;
    }
    button:disabled{
      opacity:.4;
      cursor:default;
    }
    table{
      width:100%;
      border-collapse:collapse;
      margin-top:16px;
      background:#050505;
      border-radius:10px;
      overflow:hidden;
      border:1px solid var(--table-border);
    }
    th,td{
      padding:8px 10px;
      border-bottom:1px solid var(--table-border);
      text-align:right;
      font-size:14px;
    }
    th:first-child,td:first-child{text-align:left}
    thead{
      background:#111111;
      color:var(--fg-soft);
    }
    tbody tr:hover{
      background:#111111;
    }
    tfoot td{
      font-weight:700;
      background:#111111;
    }
    .spinner{
      margin-left:8px;
      font-size:13px;
      color:var(--fg-soft);
    }
    .toast{
      margin-top:8px;
      padding:8px 10px;
      background:var(--error-bg);
      color:var(--error-fg);
      border-radius:8px;
      font-size:13px;
      border:1px solid #660000;
    }
    #resultado[aria-busy="true"]{
      opacity:0.6;
    }
    /* Sección de receta */
    .recipe{
      margin:24px auto 0;
      padding:16px 14px;
      border-radius:10px;
      border:1px solid var(--border);
      background:#050505;
      font-size:14px;
      line-height:1.5;
      max-width:900px;
      box-sizing:border-box;
    }
    .recipe h2{
      margin:0 0 10px 0;
      color:var(--fg-soft);
      font-size:18px;
    }
    .recipe h3{
      margin:14px 0 6px 0;
      color:var(--fg-soft);
      font-size:15px;
    }
    .recipe ul,
    .recipe ol{
      padding-left:18px;
      margin:4px 0 10px;
    }
    .recipe li{
      margin-bottom:4px;
    }
    .recipe small{
      opacity:0.85;
    }
    .farewell{
      margin-top:20px;
    }
    .farewell-img{
      display:block;
      width:100%;
      max-width:100%;
      height:auto;
      border:2px solid var(--border);
      border-radius:10px;
      image-rendering:pixelated;
    }
    @media (max-width: 600px){
      body{
        margin:20px auto;
        padding:0 8px;
      }
      form{
        flex-direction:column;
        align-items:stretch;
      }
      button{
        width:100%;
        text-align:center;
      }
    }
  </style>
</head>
<body>
  <h1>Calculadora de Discada Norteña - ccDN.1</h1>

  <form id="calcForm"
        hx-post="/hx/calc"
        hx-target="#resultado"
        hx-swap="innerHTML"
        hx-indicator="#spinner"
        hx-trigger="submit, keyup changed delay:500ms from:#personas from:#gpp">
    <label>Personas
      <input id="personas" name="personas" type="number" value="10" min="1" required>
    </label>
    <label>Gramos por persona
      <input id="gpp" name="gpp" type="number" value="250" min="50" step="10" required>
    </label>
    <button type="submit" id="btnCalc">Calcular</button>
    <span id="spinner" class="spinner" style="display:none;">Calculando…</span>
  </form>

  <div id="resultado" aria-live="polite" aria-busy="false">
    <!-- Aquí HTMX inyecta la tabla -->
  </div>

  <section class="recipe">
    <h2>🍽️ Preparación de la Discada</h2>

    <h3>Ingredientes (Proporciones base)</h3>
    <ul>
      <li>Tocineta (Tocino a granel)</li>
      <li>Salchicha para asar</li>
      <li>Jamón de pierna (o sustituto como chuleta/lomo de cerdo ahumado)</li>
      <li>Cebolla</li>
      <li>Pulpa de res picada (carne de res para guisar)</li>
      <li>Aceite de su preferencia</li>
      <li>Sal y pimienta (o sazonador al gusto)</li>
      <li>Cerveza</li>
      <li>Jugo de tomate (puré de tomate o jugo de verduras)</li>
      <li>Cilantro fresco</li>
    </ul>

    <h3>Instrucciones</h3>
    <ol>
      <li><strong>Mise en Place (Preparación inicial)</strong><br>
        Picar el tocino, la salchicha, el jamón (o sustituto) y la cebolla en cubos uniformes.  
        Reservar los ingredientes por separado.
      </li>
      <li><strong>Sofrito de carnes frías</strong><br>
        Calentar un disco de arado (o sartén grande) y añadir el aceite de su preferencia.  
        Incorporar las carnes frías (jamón, salchicha y tocino) y sofreír a fuego alto hasta que estén doradas.
      </li>
      <li><strong>Cocción de la carne de res</strong><br>
        Agregar la pulpa de res a la mezcla.  
        Sazonar con sal, pimienta o el sazón de su elección, considerando que posteriormente se añadirá puré o jugo de tomate.  
        Sofreír la carne hasta que el líquido o agua que suelta la pulpa se haya reducido casi por completo.
      </li>
      <li><strong>Reducción con líquidos</strong><br>
        Verter la cerveza y el jugo de tomate (la mezcla debe quedar cubierta por completo).  
        Bajar el fuego a medio-bajo y cocinar, revolviendo ocasionalmente, hasta que el líquido se haya reducido y espese.
      </li>
      <li><strong>Adición del chorizo y la cebolla</strong><br>
        Cuando el líquido restante sea espeso y esté bien adherido a las carnes, abrir un espacio en el centro y añadir el chorizo para que se deshaga y se cocine en los jugos restantes, una vez listo el chorizo, incorporar la cebolla cruda picada, mezclar y  
        sofreír hasta que la cebolla esté tierna y translúcida.  
        Rectificar la sazón final (sal, pimienta o sazonador).
      </li>
      <li><strong>Servir</strong><br>
        Agregar el cilantro fresco picado.  
        Servir la discada inmediatamente en tacos de maíz o harina.  
        <small>Sugerencia: ~1 kg de tortillas por cada 8 personas.</small>
      </li>
    </ol>

    <h3>✨ Pasos opcionales y variaciones</h3>
    <p><strong>Opción Mar y Tierra</strong><br>
      Para añadir camarón (Mar y Tierra), incorpore camarón pelado crudo y previamente sazonado al mismo tiempo que la cebolla.  
      Proporción recomendada: 1 kg de camarón por cada 2 kg de pulpa de res.
    </p>
    <p><strong>Adición de vegetales</strong><br>
      Puede integrar chiles o pimientos picados.  
      Añadir al mismo tiempo que la cebolla.  
      La cantidad de chiles/pimientos es 100% al gusto.
    </p>
    <p><strong>Sustitución de jamón</strong><br>
      El jamón puede ser reemplazado por chuletas o lomo de cerdo ahumado picado.
    </p>

    <div class="farewell">
      <img src="/static/eldorado.png" alt="Desierto pixel art" class="farewell-img">
    </div>
  </section>

  <script>
    // Cambiar texto del botón a "Calculando…" mientras HTMX hace la petición
    document.addEventListener('htmx:beforeRequest', function (evt) {
      if (evt.target && evt.target.id === 'calcForm') {
        var btn = document.getElementById('btnCalc');
        if (btn) {
          btn.dataset.originalText = btn.dataset.originalText || btn.textContent;
          btn.textContent = 'Calculando…';
          btn.disabled = true;
        }
        var res = document.getElementById('resultado');
        if (res) res.setAttribute('aria-busy', 'true');
      }
    });

    document.addEventListener('htmx:afterRequest', function (evt) {
      if (evt.target && evt.target.id === 'calcForm') {
        var btn = document.getElementById('btnCalc');
        if (btn) {
          btn.textContent = btn.dataset.originalText || 'Calcular';
          btn.disabled = false;
        }
        var res = document.getElementById('resultado');
        if (res) res.setAttribute('aria-busy', 'false');
      }
    });
  </script>
</body>
</html>`

// Tabla parcial que HTMX inyecta en #resultado
var tableTpl = template.Must(template.New("table").Parse(`
<table>
  <thead>
    <tr>
      <th>Ingrediente</th>
      <th>Gramos</th>
      <th>Unidades</th>
      <th>Precio Por Kg</th>
      <th>Precio Unitario</th>
      <th>Costo</th>
    </tr>
  </thead>
  <tbody>
  {{range .Items}}
    <tr>
      <td><a href="{{.URL}}" target="_blank" rel="noopener noreferrer">{{.Name}}</a></td>
      <td style="text-align:right">{{printf "%.0f" .GramsNeeded}} g</td>
      <td style="text-align:right">
        {{- if eq .Name "Cerveza" -}}
          {{.UnitsNeeded}} lat • {{.PurchasedUnits}} six
        {{- else if eq .Name "Jugo de verduras V8" -}}
          {{.PurchasedUnits}} lat
        {{- else if eq .Name "Salchicha p/Asar" -}}
          {{printf "%.0f" .GramsNeeded}} g → {{.PurchasedUnits}} pkg
        {{- else if eq .Name "Chorizo" -}}
          {{printf "%.0f" .GramsNeeded}} g → {{.PurchasedUnits}} pkg
        {{- else if eq .Name "Cebolla blanca" -}}
          {{.UnitsNeeded}} pza
        {{- end -}}
      </td>
      <td style="text-align:right">
        {{if gt .PricePerKg 0.0}}${{printf "%.2f" .PricePerKg}}{{else}}-{{end}}
      </td>
      <td style="text-align:right">
        {{if gt .UnitPrice 0.0}}${{printf "%.2f" .UnitPrice}}{{else}}-{{end}}
      </td>
      <td style="text-align:right">${{printf "%.2f" .Cost}}</td>
    </tr>
  {{end}}
  </tbody>
  <tfoot>
    <tr>
      <td colspan="5" style="text-align:right">Total ({{.Currency}})</td>
      <td style="text-align:right"><strong>${{printf "%.2f" .TotalCosto}}</strong></td>
    </tr>
  </tfoot>
</table>
`))

// -------------------- HTTP --------------------

func main() {
	ginMode := mustEnv("GIN_MODE", "debug")
	if ginMode == "release" {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.Default()
	r.SetTrustedProxies(nil)

	// Servir estáticos (imagen de despedida, etc.)
	r.Static("/static", "./static")

	// Página principal HTMX
	r.GET("/", func(c *gin.Context) {
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.String(200, indexPageHTML)
	})

	// Endpoint HTMX: devuelve SOLO la tabla HTML
	r.POST("/hx/calc", func(c *gin.Context) {
		personas := atoiQ(c.PostForm("personas"))
		gpp := atoiQ(c.PostForm("gpp"))
		res, err := calcFor(personas, gpp)
		if err != nil {
			log.Println("calc error:", err)
			c.String(http.StatusBadRequest, `<div class="toast">Error: %s</div>`, template.HTMLEscapeString(err.Error()))
			return
		}
		c.Header("Content-Type", "text/html; charset=utf-8")
		if err := tableTpl.Execute(c.Writer, res); err != nil {
			log.Println("error ejecutando template:", err)
		}
	})

	// Endpoint JSON por si lo sigues usando
	r.GET("/api/calc", func(c *gin.Context) {
		personas := atoiQ(c.Query("personas"))
		gpp := atoiQ(c.Query("gpp"))
		res, err := calcFor(personas, gpp)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, res)
	})

	port := mustEnv("PORT", "8080")
	log.Printf("Go app escuchando en :%s", port)
	if err := r.Run(":" + port); err != nil {
		log.Fatal(err)
	}
}

func atoiQ(s string) int {
	var n int
	fmt.Sscanf(strings.TrimSpace(s), "%d", &n)
	return n
}
