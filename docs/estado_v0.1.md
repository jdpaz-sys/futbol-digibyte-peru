# Estado del Proyecto — Fútbol Digibyte Perú (v0.1)

- **Fase actual:** Fase 1 (Fase 1A + Fase 1B: Infraestructura Local, Esquema SQL y Núcleo Backend Go) — **Completada** (pendiente commit de Fase 1B en `develop`).
- **Autor:** Juan David Paz Pastor (Digibyte Perú)
- **Última actualización:** 2026-11-25
- **Autoría Git:** `jdpaz-sys` (rama activa: `develop`)

## Documentos de Contexto Vigentes

1. `docs/srs/SRS - APK Móvil Fútbol - Digibyte Perú (v0.1).pdf` — SRS funcional aprobado (fuente de verdad).
2. `docs/reglas/agente_v0.1.md` — Reglas operativas, Git, `pnpm`, paleta Digibyte y compuertas de verificación.
3. `docs/reglas/requisitos_v0.1.md` — Síntesis técnica de RF-01..16, RNF-01..08, RN-01..20, 8 estados y CU-01..14.
4. `docs/arquitectura_v0.2.md` — Contrato técnico definitivo (Next.js + Go + PostgreSQL 16/PostGIS + Valkey + S3 + Caddy).

## Hitos Cerrados (Fases 0, 1A y 1B)

- **Fase 0:** SRS sintetizado, reglas operativas y arquitectura desacoplada aprobada (`v0.1-planificacion`).
- **Fase 1A — Infraestructura Dev y SQL:** `infra/docker-compose.dev.yml` (`healthy`: PostgreSQL 16 + PostGIS 3.4 en `America/Lima` y Valkey 8 autenticado) y migración `000001_init` (15 tablas, FKs multi-tenant, PostGIS WGS84, GIST antisolapamiento/anticolición; cero GPS de jugador).
- **Fase 1B — Núcleo Backend Go:** esqueleto plano en `backend/` (`cmd/api`, `internal/db`, `internal/http`, `internal/modules/auth`), pool `pgx/v5`, cliente Valkey 8, `GET /api/v1/health`, módulo `auth` (`register`, `login`, `refresh` con rotación atómica `GETDEL`, `logout`, `me`, Argon2id + JWT HS256) y middlewares `RequireAuth`, `RequireRole`, `RequireTenant` y `RateLimit` en Valkey. Verificado en verde (`go vet`, `go test`, `go build` y flujo E2E en vivo).

## Siguiente Paso Exacto (Fase 2)

1. Aprobar commit de Fase 1B en `develop`.
2. Implementar módulos `admin` (complejos, planes y suscripciones SaaS, métricas globales) y `complejos` (perfil, canchas, coordenadas PostGIS + geocodificación inversa en Valkey, galería S3/MinIO).
3. Implementar módulo `tarifas` (horarios semanales, franjas dinámicas, horas muertas, cupones), términos y medios de cobro propios (RF-01..RF-08).
4. Incorporar MinIO y Caddy al entorno local cuando se habiliten subidas S3 y enrutamiento inverso.
