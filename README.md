
# Calculadora de Discada Norteña (lista para correr)

Este paquete trae **todo listo** para que corra en tu computadora sin programar. Solo necesitas **Docker Desktop**.

## ✅ Qué hace
- Calcula cantidades de ingredientes de **Discada Norteña** según **Personas** y **Gramos por persona**.
- **Scrapea precios en tiempo real** de alsuper.com y los **cachea 5 minutos** para que el sitio responda rápido si haces varios cálculos seguidos.
- Ajusta cantidades según las **proporciones de tu XLSX**.
- Trata la **cerveza** como **six-pack** y el **V8** como **lata** individual, como pediste.

## 🧰 Requisitos
1) Instala **Docker Desktop** (Windows o Mac): https://www.docker.com/products/docker-desktop/
2) Abre Docker Desktop y asegúrate que quede ejecutándose.

## ▶️ Cómo arrancar (paso a paso, sin programar)
1. Descarga este ZIP y descomprímelo (por ejemplo en tu Escritorio).
2. Abre una **terminal**:
   - **Windows**: busca “PowerShell” y ábrelo.
   - **Mac**: abre “Terminal” desde Spotlight.
3. En la terminal, entra a la carpeta donde descomprimiste el ZIP. Ejemplo (ajusta la ruta real):
   - Windows (PowerShell):
     ```powershell
     cd "$HOME\Desktop\discada-calculadora"
     ```
   - Mac:
     ```bash
     cd "$HOME/Desktop/discada-calculadora"
     ```
4. Ejecuta este comando (solo la **primera vez** tarda un poco porque construye las imágenes):
   ```bash
   docker compose up --build
   ```
5. Cuando veas que dice que está “escuchando” en el puerto 8080, abre tu navegador en:
   - http://localhost:8080

6. En la página:
   - Escribe **Personas** y **Gramos por persona** (por ejemplo 10 y 250).
   - Presiona **Calcular**.
   - Verás la tabla con **cantidades**, **precio por KG**, **precio unitario** (si aplica), y el **total**.

> 💡 **La primera consulta puede tardar** porque el scraper abre cada página de Alsuper. Luego las siguientes consultas en los próximos **5 minutos** serán rápidas (por la caché).

## 🔄 Si notas que alguna página no muestra precio
Algunas páginas pueden cargar el precio con JavaScript. Si el precio no aparece, puedes **activar el modo Playwright** (un navegador sin cabeza) en el scraper:

1. Edita el archivo `scraper/main.py` y cambia:
   ```python
   USE_PLAYWRIGHT_FALLBACK = False
   ```
   por:
   ```python
   USE_PLAYWRIGHT_FALLBACK = True
   ```
2. Edita `scraper/Dockerfile` y **descomenta** la línea que instala Playwright:
   ```dockerfile
   # RUN playwright install --with-deps chromium
   ```
   quita el `#` del inicio:
   ```dockerfile
   RUN playwright install --with-deps chromium
   ```
3. Vuelve a construir y correr:
   ```bash
   docker compose up --build
   ```

## 🧪 Consejos
- Si cambias algo y quieres “reiniciar limpio”, presiona `Ctrl + C` en la terminal para detener, y luego:
  ```bash
  docker compose up --build
  ```
- La **caché** se limpia sola a los 5 minutos.

## 📁 Estructura
```
discada-calculadora/
├─ docker-compose.yml
├─ README.md
├─ go-app/
│  ├─ Dockerfile
│  ├─ go.mod
│  └─ main.go
└─ scraper/
   ├─ Dockerfile
   ├─ main.py
   └─ requirements.txt
```

¡Listo! Si quieres que lo suba a un repositorio o que personalice el estilo de la página, dímelo y te lo ajusto.
