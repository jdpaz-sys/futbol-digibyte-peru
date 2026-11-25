# Reglas Operativas del Agente — Fútbol Digibyte Perú (v0.1)

Reglas obligatorias para toda sesión de Antigravity (Carbon y Neon) en este repositorio. Antes de modificar código o documentación, leer `docs/estado_v0.1.md` y los documentos vigentes allí listados.

## 1. Disciplina de Repositorio y Estructura
- **Cero archivos innecesarios:** prohibido crear carpetas vacías, archivos placeholder, helpers "por si acaso" o código muerto.
- **Estructura plana sin sobreingeniería:** respetar la arquitectura modular plana definida en `docs/arquitectura_v0.2.md`. No introducir capas redundantes ni abstracciones especulativas.
- **Fuente de verdad funcional:** toda decisión de negocio debe alinearse a `docs/reglas/requisitos_v0.1.md` y al SRS aprobado en `docs/srs/SRS - APK Móvil Fútbol - Digibyte Perú (v0.1).pdf`.

## 2. Arquitectura Desacoplada Real
- **Backend en Go, Frontend en Next.js:** Caddy enruta `/api/v1/*` hacia Go y `/*` hacia Next.js.
- **Prohibido crear proxies en Next.js:** nunca crear endpoints en `frontend/src/app/api/*` para reenviar peticiones a Go. El navegador/cliente consume directamente `/api/v1/*`.
- **Fuente de verdad transaccional:** PostgreSQL 16 + PostGIS resuelve aislamiento por `complejo_id` y anticolisión (`EXCLUDE USING gist`). Valkey es apoyo de caché, sesiones, rate limiting y TTL; nunca reemplaza las garantías ACID.

## 3. Reglas de Frontend y Estándar Visual
- **Gestor de paquetes exclusivo:** usar únicamente `pnpm`. Prohibido ejecutar `npm` o `yarn` o generar `package-lock.json` / `yarn.lock`.
- **Stack UI:** Next.js (App Router, TypeScript, PWA), Tailwind CSS, Radix UI / shadcn, Motion y Lucide Icons; mapas con MapLibre GL JS + MapTiler + OSRM.
- **Diseño atractivo, fluido y mobile-first:** priorizar microinteracciones limpias, legibilidad en móvil (flujo de reserva del jugador y validación en un clic del dueño) y cero límites artificiales de calidad visual.
- **Paleta corporativa Digibyte (estricta):**
  - Base oscura / encabezados: Navy `#0F172A`
  - Primario / acciones clave: Azul cobalto `#2563EB`
  - Superficies y neutros: Blanco `#FFFFFF` y escala pizarra (`slate`)
  - Semánticos solo para estados operativos (éxito, alerta, error)
  - **PROHIBIDO** usar morado, violeta o índigo (`purple`, `violet`, `indigo`) en temas, gradientes, bordes o íconos.

## 4. Documentación Versionada y Ahorro de Tokens
- **Nunca inflar un `.md` indefinidamente:** mantener cada documento conciso, tabulado y dentro de su techo de líneas.
- **Versionado explícito:** cuando un documento crezca por cambios de fase o decisiones técnicas, crear nueva versión (`_v0.1.md` -> `_v0.2.md`) y actualizar la referencia en `docs/estado_v*.md`.
- **No duplicar el SRS:** conservar únicamente el PDF aprobado en `docs/srs/`.

## 5. Flujo Git Seguro (No Destructivo)
- **Autoría local:** los commits deben registrarse bajo `jdpaz-sys` (`user.name` y `user.email` locales configurados y verificados).
- **Preparación explícita:** prohibido `git add .` o `git add -A`; siempre preparar rutas explícitas (`git add ruta/archivo`).
- **Commits semánticos:** usar prefijos `docs:`, `feat:`, `fix:`, `chore:`, `refactor:`, `test:` con mensajes breves en español o inglés consistente.
- **Comandos prohibidos:** nunca ejecutar `git reset --hard`, `git push --force`, `git push -f`, `git clean -fd` ni reescribir historia publicada.

## 6. Compuertas de Verificación Obligatorias
Cuando exista código en el repositorio, ningún cambio se da por terminado sin pasar en verde:
- **Backend (Go):** `go vet ./...` y `go build ./...` (y `go test ./...` cuando existan pruebas).
- **Frontend (Next.js):** `pnpm build` (y lint/typecheck configurados).
- **Privacidad y negocio:** verificar que jamás se persistan coordenadas GPS del jugador ni se intermedien cobros de reservas.
