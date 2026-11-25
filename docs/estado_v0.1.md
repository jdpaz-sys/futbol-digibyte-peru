# Estado del Proyecto — Fútbol Digibyte Perú (v0.1)

- **Fase actual:** Fase 1A (Infraestructura Local + Esquema SQL Base) — **Completada**.
- **Autor:** Juan David Paz Pastor (Digibyte Perú)
- **Última actualización:** 2026-11-25
- **Autoría Git:** `jdpaz-sys` (rama activa: `develop`)

## Documentos de Contexto Vigentes

1. `docs/srs/SRS - APK Móvil Fútbol - Digibyte Perú (v0.1).pdf` — SRS funcional aprobado (fuente de verdad).
2. `docs/reglas/agente_v0.1.md` — Reglas operativas, Git, `pnpm`, paleta Digibyte y compuertas de verificación.
3. `docs/reglas/requisitos_v0.1.md` — Síntesis técnica de RF-01..16, RNF-01..08, RN-01..20, 8 estados y CU-01..14.
4. `docs/arquitectura_v0.2.md` — Contrato técnico definitivo (Next.js + Go + PostgreSQL 16/PostGIS + Valkey + S3 + Caddy).

## Hitos Cerrados (Fases 0 y 1A)

- **Fase 0:** SRS sintetizado, reglas operativas y arquitectura desacoplada aprobada (`v0.1-planificacion`).
- **Fase 1A — Infraestructura Dev:** `infra/docker-compose.dev.yml` operativo y `healthy` con PostgreSQL 16 + PostGIS 3.4 (`America/Lima`) y Valkey 8 autenticado (`--requirepass`, `128mb` LRU), más `infra/.env.example`.
- **Fase 1A — Esquema SQL (`000001_init`):** 15 tablas verificadas en vivo (`up`/`down` limpio, FKs multi-tenant compuestas, PostGIS WGS84, antisolapamiento GIST en `tarifas_franja` y anticolisión GIST en `reservas` sobre rango `[)` estricto, planes SaaS mensuales/anuales/prueba y `push_subscriptions` VAPID; cero GPS de jugador).

## Siguiente Paso Exacto (Fase 1B)

1. Inicializar `backend/` plano en Go (`go.mod`, `cmd/api`, `internal/db`, `internal/http`) con pool `pgx/v5` y cliente Valkey.
2. Implementar endpoint `/api/v1/health`, autenticación base (JWT + sesiones en Valkey), RBAC y middleware de aislamiento `complejo_id`.
3. Incorporar MinIO y Caddy al entorno local cuando se habiliten subidas S3 y enrutamiento inverso.
4. Validar compuertas `go vet ./...` y `go build ./...`.
