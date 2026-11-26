# Estado del Proyecto — Fútbol Digibyte Perú (v0.1)

- **Fase actual:** Fase 3C (Multimedia pública/privada, Invalidación Valkey, Perfil/Contraseña Auth, Worker CU-14 completo, Web Push VAPID opcional y CORS) — **Backend 100% cerrado** (pendiente commit en `develop`).
- **Autor:** Juan David Paz Pastor (Digibyte Perú)
- **Última actualización:** 2026-11-25
- **Autoría Git:** `jdpaz-sys` (rama activa: `develop`)

## Documentos de Contexto Vigentes

1. `docs/srs/SRS - APK Móvil Fútbol - Digibyte Perú (v0.1).pdf` — SRS funcional aprobado (fuente de verdad).
2. `docs/reglas/agente_v0.1.md` — Reglas operativas, Git, `pnpm`, paleta Digibyte y compuertas de verificación.
3. `docs/reglas/requisitos_v0.1.md` — Síntesis técnica de RF-01..16, RNF-01..08, RN-01..20, 8 estados y CU-01..14.
4. `docs/arquitectura_v0.2.md` — Contrato técnico definitivo (Next.js + Go + PostgreSQL 16/PostGIS + Valkey + S3 + Caddy).

## Hitos Cerrados (Backend 100%: Fases 0, 1A, 1B, 2A, 2B, 2C, 3A, 3B y 3C)

- **Fase 0:** SRS sintetizado, reglas operativas y arquitectura desacoplada aprobada (`v0.1-planificacion`).
- **Fase 1A — Infraestructura Dev y SQL:** `infra/docker-compose.dev.yml` (PostgreSQL 16 + PostGIS 3.4 en `America/Lima` y Valkey 8) y migración `000001_init` (15 tablas, FKs multi-tenant, PostGIS WGS84, GIST anticolisión; cero GPS de jugador).
- **Fase 1B — Núcleo Backend Go:** `cmd/api`, `internal/db`, `internal/http`, `internal/modules/auth` (Argon2id + JWT HS256, refresh `GETDEL`) y middlewares `RequireAuth`, `RequireRole`, `RequireTenant` y `RateLimit`.
- **Fase 2A — Módulo B2B `suscripciones` (`SUPER_ADMIN`):** `-seed-admin` y `/api/v1/admin/*` (planes, alta atómica complejo+dueño, renovaciones, estados RN-02 y métricas Valkey).
- **Fase 2B — Módulo `complejos` (`DUENO`):** cifrado AES-256-GCM y 21 endpoints `/api/v1/dueno/*` (perfil/PostGIS/reglas, publicación RN-03, canchas, horarios, tarifas GIST, cupones RN-09 y medios de cobro RN-10).
- **Fase 2C — Módulo `geo` y Catálogo Público:** geocodificación inversa cacheada, `GET /api/v1/public/complejos` (`ST_DWithin`/`ST_Distance`, degradación RN-19) y detalle por slug (RN-02).
- **Fase 3A — `reservas` (Jugador + Storage + Worker):** `LocalFileStorage` privado, disponibilidad/cotizador mixto RN-08/RN-09, Hold GIST (`23P01` -> `409`), comprobante manual (`POR_VALIDAR`), mis reservas/evidencia ACL y worker de expiración 30s (`FOR UPDATE SKIP LOCKED`).
- **Fase 3B — `reservas` (Dueño + Notificaciones/Push) y `reportes`:** agenda `/api/v1/dueno/reservas` con `alerta_demora` >30 min (Decisión 3), aprobar/rechazar, externas y bloqueos GIST (RN-06), búsqueda QR, check-in y No-Show, cancelaciones RN-13/RN-14, reprogramación in-place Decisión 4, `/api/v1/notificaciones` + `/api/v1/push-subscriptions` y `GET /api/v1/dueno/reportes/resumen` (RF-16).
- **Fase 3C — Cierre Operativo del Backend (cero migraciones nuevas):** galería `/dueno/fotos`, foto de cancha, QR Yape/Plin y servicio público seguro `GET /api/v1/public/media/*` (prefijo `public/`, MIME real, bloqueo de vouchers privados); invalidación inmediata de caché Valkey por slug; `PATCH /auth/me` y `POST /auth/change-password` con revocación de refresh tokens; worker CU-14 cada 5 min (vencer suscripciones RN-02, avisos `<=3d` y recordatorios `<2h` idempotentes); Web Push VAPID opcional (`GET /api/v1/public/push-config`, desactivación `410 Gone`) y CORS listo para Next.js.

## Siguiente Paso Exacto (Fase 4 — Frontend Next.js)

1. Aprobar commit de Fase 3C en `develop`.
2. Iniciar Fase 4: Planificación visual y construcción del Frontend PWA Next.js (App Router, TypeScript, Tailwind, Radix/shadcn, MapLibre + OSRM, paleta Digibyte Navy/Cobalto) consumiendo directamente `/api/v1/*`.
