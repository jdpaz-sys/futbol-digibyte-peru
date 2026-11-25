# Estado del Proyecto — Fútbol Digibyte Perú (v0.1)

- **Fase actual:** Fase 2A (Módulo B2B `suscripciones` del Super Admin + Bootstrap `SUPER_ADMIN`) — **Completada** (pendiente commit en `develop`).
- **Autor:** Juan David Paz Pastor (Digibyte Perú)
- **Última actualización:** 2026-11-25
- **Autoría Git:** `jdpaz-sys` (rama activa: `develop`)

## Documentos de Contexto Vigentes

1. `docs/srs/SRS - APK Móvil Fútbol - Digibyte Perú (v0.1).pdf` — SRS funcional aprobado (fuente de verdad).
2. `docs/reglas/agente_v0.1.md` — Reglas operativas, Git, `pnpm`, paleta Digibyte y compuertas de verificación.
3. `docs/reglas/requisitos_v0.1.md` — Síntesis técnica de RF-01..16, RNF-01..08, RN-01..20, 8 estados y CU-01..14.
4. `docs/arquitectura_v0.2.md` — Contrato técnico definitivo (Next.js + Go + PostgreSQL 16/PostGIS + Valkey + S3 + Caddy).

## Hitos Cerrados (Fases 0, 1A, 1B y 2A)

- **Fase 0:** SRS sintetizado, reglas operativas y arquitectura desacoplada aprobada (`v0.1-planificacion`).
- **Fase 1A — Infraestructura Dev y SQL:** `infra/docker-compose.dev.yml` (`healthy`: PostgreSQL 16 + PostGIS 3.4 en `America/Lima` y Valkey 8 autenticado) y migración `000001_init` (15 tablas, FKs multi-tenant, PostGIS WGS84, GIST antisolapamiento/anticolisión; cero GPS de jugador).
- **Fase 1B — Núcleo Backend Go:** `cmd/api`, `internal/db`, `internal/http`, `internal/modules/auth` (`register`, `login`, `refresh` `GETDEL`, `logout`, `me`, Argon2id + JWT HS256) y middlewares `RequireAuth`, `RequireRole`, `RequireTenant` y `RateLimit` en Valkey.
- **Fase 2A — Módulo B2B `suscripciones` (`SUPER_ADMIN`):** flag `-seed-admin` idempotente en `cmd/api/main.go` y módulo `internal/modules/suscripciones` bajo `/api/v1/admin/*` (catálogo `planes`, alta atómica `complejo + DUENO + snapshot`, listado filtrable/detalle, renovación con extensión de vigencia en `America/Lima`, cambio de estado preservando reservas confirmadas RN-02 y `metricas` globales con caché corto e invalidación en Valkey). Verificado en verde (`go vet`, `go test`, `go build` y E2E en vivo).

## Siguiente Paso Exacto (Fase 2B)

1. Aprobar commit de Fase 2A en `develop`.
2. Implementar módulo `complejos` del `DUENO` (perfil, canchas, coordenadas PostGIS + geocodificación inversa cacheada en Valkey, galería S3/MinIO y condiciones de publicación RN-03).
3. Implementar módulo `tarifas` (horarios semanales, franjas dinámicas, horas muertas, cupones), términos y medios de cobro propios (RF-03..RF-08).
4. Incorporar MinIO y Caddy al entorno local cuando se habiliten subidas S3 y enrutamiento inverso.
