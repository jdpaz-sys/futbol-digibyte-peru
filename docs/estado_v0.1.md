# Estado del Proyecto — Fútbol Digibyte Perú (v0.1)

- **Fase actual:** Fase 3B (Operación diaria del Dueño, Cancelaciones/Reprogramaciones, Notificaciones/Web Push y Reportes) — **Fase 3 y Backend 100% Completados** (pendiente commit en `develop`).
- **Autor:** Juan David Paz Pastor (Digibyte Perú)
- **Última actualización:** 2026-11-25
- **Autoría Git:** `jdpaz-sys` (rama activa: `develop`)

## Documentos de Contexto Vigentes

1. `docs/srs/SRS - APK Móvil Fútbol - Digibyte Perú (v0.1).pdf` — SRS funcional aprobado (fuente de verdad).
2. `docs/reglas/agente_v0.1.md` — Reglas operativas, Git, `pnpm`, paleta Digibyte y compuertas de verificación.
3. `docs/reglas/requisitos_v0.1.md` — Síntesis técnica de RF-01..16, RNF-01..08, RN-01..20, 8 estados y CU-01..14.
4. `docs/arquitectura_v0.2.md` — Contrato técnico definitivo (Next.js + Go + PostgreSQL 16/PostGIS + Valkey + S3 + Caddy).

## Hitos Cerrados (Backend 100%: Fases 0, 1A, 1B, 2A, 2B, 2C, 3A y 3B)

- **Fase 0:** SRS sintetizado, reglas operativas y arquitectura desacoplada aprobada (`v0.1-planificacion`).
- **Fase 1A — Infraestructura Dev y SQL:** `infra/docker-compose.dev.yml` (PostgreSQL 16 + PostGIS 3.4 en `America/Lima` y Valkey 8) y migración `000001_init` (15 tablas, FKs multi-tenant, PostGIS WGS84, GIST anticolisión; cero GPS de jugador).
- **Fase 1B — Núcleo Backend Go:** `cmd/api`, `internal/db`, `internal/http`, `internal/modules/auth` (Argon2id + JWT HS256, refresh `GETDEL`) y middlewares `RequireAuth`, `RequireRole`, `RequireTenant` y `RateLimit`.
- **Fase 2A — Módulo B2B `suscripciones` (`SUPER_ADMIN`):** `-seed-admin` y `/api/v1/admin/*` (planes, alta atómica complejo+dueño, renovaciones, estados RN-02 y métricas Valkey).
- **Fase 2B — Módulo `complejos` (`DUENO`):** cifrado AES-256-GCM y 21 endpoints `/api/v1/dueno/*` (perfil/PostGIS/reglas, publicación RN-03, canchas, horarios, tarifas GIST, cupones RN-09 y medios de cobro RN-10).
- **Fase 2C — Módulo `geo` y Catálogo Público:** geocodificación inversa cacheada, `GET /api/v1/public/complejos` (`ST_DWithin`/`ST_Distance`, degradación RN-19) y detalle por slug (RN-02).
- **Fase 3A — `reservas` (Jugador + Storage + Worker):** `LocalFileStorage` privado, disponibilidad/cotizador mixto RN-08/RN-09, Hold GIST (`23P01` -> `409`), comprobante manual (`POR_VALIDAR`), mis reservas/evidencia ACL y worker de expiración 30s (`FOR UPDATE SKIP LOCKED`).
- **Fase 3B — `reservas` (Dueño + Notificaciones/Push) y `reportes`:** agenda `/api/v1/dueno/reservas` con `alerta_demora` >30 min (Decisión 3), aprobar con QR/rechazar con motivo y reversión de cupón, reservas externas y bloqueos GIST (RN-06), búsqueda QR, check-in con trazabilidad financiera y No-Show, cancelaciones RN-13/RN-14 + reembolso resuelto, reprogramación in-place Decisión 4 con recálculo y auditoría, bandeja `/api/v1/notificaciones` + `/api/v1/push-subscriptions` (Decisión 6) y `GET /api/v1/dueno/reportes/resumen` (RF-16, finanzas/conteos/ocupación).

## Siguiente Paso Exacto (Fase 4 — Frontend Next.js)

1. Aprobar commit de Fase 3B en `develop`.
2. Iniciar Fase 4: Frontend PWA Next.js (App Router, TypeScript, Tailwind, Radix/shadcn, MapLibre + OSRM, paleta Digibyte Navy/Cobalto) consumiendo directamente `/api/v1/*`.
