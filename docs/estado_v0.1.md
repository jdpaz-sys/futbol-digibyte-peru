# Estado del Proyecto — Fútbol Digibyte Perú (v0.1)

- **Fase actual:** Fase 0 (Planificación y Preparación del Repositorio) — **Completada**.
- **Autor:** Juan David Paz Pastor (Digibyte Perú)
- **Última actualización:** 2026-11-25
- **Autoría Git:** `jdpaz-sys`

## Documentos de Contexto Vigentes

1. `docs/srs/SRS - APK Móvil Fútbol - Digibyte Perú (v0.1).pdf` — SRS funcional aprobado (fuente de verdad).
2. `docs/reglas/agente_v0.1.md` — Reglas operativas, Git, `pnpm`, paleta Digibyte y compuertas de verificación.
3. `docs/reglas/requisitos_v0.1.md` — Síntesis técnica de RF-01..16, RNF-01..08, RN-01..20, 8 estados y CU-01..14.
4. `docs/arquitectura_v0.2.md` — Contrato técnico definitivo (Next.js + Go + PostgreSQL 16/PostGIS + Valkey + S3 + Caddy).

## Decisiones Cerradas en Fase 0

- Repositorio clonado limpio desde `https://github.com/jdpaz-sys/futbol-digibyte-peru.git` (rama `main` sin commits previos).
- SRS v0.1 archivado como fuente única y sintetizado operativamente sin generar archivos de código prematuros.
- Arquitectura desacoplada cerrada sin proxies en Next.js y con garantía anticolisión en PostgreSQL (`EXCLUDE USING gist`).
- Ajuste previo a Fase 1A incorporado en `develop`: catálogo de planes SaaS configurables (`MENSUAL`, `ANUAL` y `PRUEBA`) con trazabilidad de pagos/renovaciones del Super Admin.

## Siguiente Paso Exacto (Fase 1)

1. Crear `infra/docker-compose.dev.yml` (PostgreSQL 16 + PostGIS, Valkey, MinIO, Caddy).
2. Inicializar `backend/` plano en Go (`go.mod`, `cmd/api`, `internal/db`, `internal/http`) con conexión `pgx/v5` y Valkey.
3. Escribir migraciones SQL iniciales (`backend/migrations/`) cubriendo multi-tenancy (`complejo_id`), PostGIS y restricción GIST de reservas.
4. Validar compuertas `go vet ./...` y `go build ./...`.
