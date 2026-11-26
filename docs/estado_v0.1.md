# Estado del Proyecto — Fútbol Digibyte Perú (v0.1)

- **Fase actual:** Fase 3A (Motor de Reservas — Flujo del Jugador, Storage Privado y Worker de Expiración) — **Completada** (pendiente commit en `develop`).
- **Autor:** Juan David Paz Pastor (Digibyte Perú)
- **Última actualización:** 2026-11-25
- **Autoría Git:** `jdpaz-sys` (rama activa: `develop`)

## Documentos de Contexto Vigentes

1. `docs/srs/SRS - APK Móvil Fútbol - Digibyte Perú (v0.1).pdf` — SRS funcional aprobado (fuente de verdad).
2. `docs/reglas/agente_v0.1.md` — Reglas operativas, Git, `pnpm`, paleta Digibyte y compuertas de verificación.
3. `docs/reglas/requisitos_v0.1.md` — Síntesis técnica de RF-01..16, RNF-01..08, RN-01..20, 8 estados y CU-01..14.
4. `docs/arquitectura_v0.2.md` — Contrato técnico definitivo (Next.js + Go + PostgreSQL 16/PostGIS + Valkey + S3 + Caddy).

## Hitos Cerrados (Fases 0, 1A, 1B, 2A, 2B, 2C y 3A)

- **Fase 0:** SRS sintetizado, reglas operativas y arquitectura desacoplada aprobada (`v0.1-planificacion`).
- **Fase 1A — Infraestructura Dev y SQL:** `infra/docker-compose.dev.yml` (`healthy`: PostgreSQL 16 + PostGIS 3.4 en `America/Lima` y Valkey 8 autenticado) y migración `000001_init` (15 tablas, FKs multi-tenant, PostGIS WGS84, GIST antisolapamiento/anticolisión; cero GPS de jugador).
- **Fase 1B — Núcleo Backend Go:** `cmd/api`, `internal/db`, `internal/http`, `internal/modules/auth` (`register`, `login`, `refresh` `GETDEL`, `logout`, `me`, Argon2id + JWT HS256) y middlewares `RequireAuth`, `RequireRole`, `RequireTenant` y `RateLimit` en Valkey.
- **Fase 2A — Módulo B2B `suscripciones` (`SUPER_ADMIN`):** `-seed-admin` idempotente y `/api/v1/admin/*` (catálogo `planes`, alta atómica `complejo + DUENO`, renovaciones en `America/Lima`, cambios de estado RN-02 y `metricas` en Valkey).
- **Fase 2B — Módulo `complejos` (`DUENO`):** cifrado AES-256-GCM en `internal/db/crypto.go` (`CREDENTIALS_ENCRYPTION_KEY`) y 21 endpoints bajo `/api/v1/dueno/*` cubriendo perfil/PostGIS/reglas, publicación RN-03 (`422`), canchas, horarios, tarifas (`23P01` -> `409`), cupones RN-09 y métodos de cobro RN-10.
- **Fase 2C — Módulo `geo` y Catálogo Público:** `GET /api/v1/dueno/geo/reverse` (Nominatim timeout 4s, redondeo 5 decimales, caché Valkey 24h), `GET /api/v1/public/complejos` (PostGIS `ST_DWithin`/`ST_Distance` + degradación por zona sin GPS RN-19, cero persistencia/logs de ubicación del jugador) y `GET /api/v1/public/complejos/{slug}` (vigencia RN-02, caché Valkey 60s y solo tipos de métodos de pago activos).
- **Fase 3A — Módulo `reservas` (Flujo Jugador + Storage + Worker):** `internal/db/storage.go` (`FileStorage` / `LocalFileStorage` en `UPLOAD_DIR`, listo para R2), disponibilidad en vivo y cotizador mixto RN-08/RN-09 (`/api/v1/public/complejos/{slug}/disponibilidad|cotizar`), Hold GIST anticolisión (`23P01` -> `409`, Decisión 2, RN-10), comprobante manual multipart PNG/JPG/WEBP <= 5 MB (`POR_VALIDAR` + notificación al dueño), `GET /api/v1/jugador/reservas[/{id}]` (Modo En Ruta, QR solo en `CONFIRMADA`/`COMPLETADA`), `GET /api/v1/comprobantes/{id}/evidencia` (ACL dueño/jugador) y worker de expiración cada 30s (`FOR UPDATE SKIP LOCKED`, reversión de cupón, Decisión 3). Verificado en verde (`go vet`, `go test`, `go build` y E2E en vivo).

## Siguiente Paso Exacto (Fase 3B)

1. Aprobar commit en `develop`.
2. Implementar Fase 3B del motor de reservas (`DUENO`): validación manual de comprobantes (aprobar con emisión QR / rechazar con motivo), reservas externas y bloqueos de mantenimiento, *check-in* con liquidación de saldo, cancelación/reprogramación/no-show y reportes operativos.
