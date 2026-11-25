# Estado del Proyecto — Fútbol Digibyte Perú (v0.1)

- **Fase actual:** Fase 2B (Módulo `complejos` — Panel de Autogestión del Dueño + cifrado AES-256-GCM) — **Completada** (pendiente commit en `develop`).
- **Autor:** Juan David Paz Pastor (Digibyte Perú)
- **Última actualización:** 2026-11-25
- **Autoría Git:** `jdpaz-sys` (rama activa: `develop`)

## Documentos de Contexto Vigentes

1. `docs/srs/SRS - APK Móvil Fútbol - Digibyte Perú (v0.1).pdf` — SRS funcional aprobado (fuente de verdad).
2. `docs/reglas/agente_v0.1.md` — Reglas operativas, Git, `pnpm`, paleta Digibyte y compuertas de verificación.
3. `docs/reglas/requisitos_v0.1.md` — Síntesis técnica de RF-01..16, RNF-01..08, RN-01..20, 8 estados y CU-01..14.
4. `docs/arquitectura_v0.2.md` — Contrato técnico definitivo (Next.js + Go + PostgreSQL 16/PostGIS + Valkey + S3 + Caddy).

## Hitos Cerrados (Fases 0, 1A, 1B, 2A y 2B)

- **Fase 0:** SRS sintetizado, reglas operativas y arquitectura desacoplada aprobada (`v0.1-planificacion`).
- **Fase 1A — Infraestructura Dev y SQL:** `infra/docker-compose.dev.yml` (`healthy`: PostgreSQL 16 + PostGIS 3.4 en `America/Lima` y Valkey 8 autenticado) y migración `000001_init` (15 tablas, FKs multi-tenant, PostGIS WGS84, GIST antisolapamiento/anticolisión; cero GPS de jugador).
- **Fase 1B — Núcleo Backend Go:** `cmd/api`, `internal/db`, `internal/http`, `internal/modules/auth` (`register`, `login`, `refresh` `GETDEL`, `logout`, `me`, Argon2id + JWT HS256) y middlewares `RequireAuth`, `RequireRole`, `RequireTenant` y `RateLimit` en Valkey.
- **Fase 2A — Módulo B2B `suscripciones` (`SUPER_ADMIN`):** `-seed-admin` idempotente y `/api/v1/admin/*` (catálogo `planes`, alta atómica `complejo + DUENO`, renovaciones en `America/Lima`, cambios de estado RN-02 y `metricas` en Valkey).
- **Fase 2B — Módulo `complejos` (`DUENO`):** cifrado AES-256-GCM en `internal/db/crypto.go` (`CREDENTIALS_ENCRYPTION_KEY`) y 21 endpoints bajo `/api/v1/dueno/*` (`RequireAuth + RequireRole("DUENO") + RequireTenant`) cubriendo perfil/coordenadas PostGIS/reglas, publicación transaccional RN-03 (`422`), canchas (baja lógica), horarios 7 días atómicos, tarifas por franja (`23P01` -> `409`), cupones RN-09 (`23505` -> `409`) y métodos de cobro RN-10 sin exposición de credenciales. Verificado en verde (`go vet`, `go test`, `go build` y E2E en vivo).

## Siguiente Paso Exacto (Fase 2C / Fase 3)

1. Aprobar commit de Fase 2B en `develop`.
2. Implementar módulo `geo` (geocodificación inversa cacheada en Valkey + exploración pública PostGIS `ST_DWithin`/`ST_Distance`, RF-04/RF-09) y subida de galería S3/MinIO.
3. Implementar módulo `reservas` y `pagos` (disponibilidad en vivo, motor tarifario mixto RN-08, Hold GIST RN-04/RN-05, validación manual RF-12, QR RF-13 y worker de expiración).
