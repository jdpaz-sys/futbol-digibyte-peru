# Estado del Proyecto — Fútbol Digibyte Perú (v0.1)

- **Fase actual:** Fase 4A.1 completada (Frontend base separado `(landing)` vs `(app)`, medios `ffmpeg` optimizados y Hero cinemático con entrada orgánica + scroll-scrubbing GSAP/Lenis) — **Backend 100% cerrado** (pendiente commit en `develop`).
- **Autor:** Juan David Paz Pastor (Digibyte Perú)
- **Última actualización:** 2026-11-26
- **Autoría Git:** `jdpaz-sys` (rama activa: `develop`)

## Documentos de Contexto Vigentes

1. `docs/srs/SRS - APK Móvil Fútbol - Digibyte Perú (v0.1).pdf` — SRS funcional aprobado (fuente de verdad).
2. `docs/reglas/agente_v0.1.md` — Reglas operativas, Git, `pnpm`, paleta Digibyte y compuertas de verificación.
3. `docs/reglas/requisitos_v0.1.md` — Síntesis técnica de RF-01..16, RNF-01..08, RN-01..20, 8 estados y CU-01..14.
4. `docs/arquitectura_v0.2.md` — Contrato técnico definitivo (Next.js + Go + PostgreSQL 16/PostGIS + Valkey + S3 + Caddy).

## Hitos Cerrados

- **Fase 0:** SRS sintetizado, reglas operativas y arquitectura desacoplada aprobada (`v0.1-planificacion`).
- **Fase 1A — Infraestructura Dev y SQL:** `infra/docker-compose.dev.yml` (PostgreSQL 16 + PostGIS 3.4 en `America/Lima` y Valkey 8) y migración `000001_init` (15 tablas, FKs multi-tenant, PostGIS WGS84, GIST anticolisión; cero GPS de jugador).
- **Fase 1B — Núcleo Backend Go:** `cmd/api`, `internal/db`, `internal/http`, `internal/modules/auth` (Argon2id + JWT HS256, refresh `GETDEL`) y middlewares `RequireAuth`, `RequireRole`, `RequireTenant` y `RateLimit`.
- **Fase 2A — Módulo B2B `suscripciones` (`SUPER_ADMIN`):** `-seed-admin` y `/api/v1/admin/*` (planes, alta atómica complejo+dueño, renovaciones, estados RN-02 y métricas Valkey).
- **Fase 2B — Módulo `complejos` (`DUENO`):** cifrado AES-256-GCM y 21 endpoints `/api/v1/dueno/*` (perfil/PostGIS/reglas, publicación RN-03, canchas, horarios, tarifas GIST, cupones RN-09 y medios de cobro RN-10).
- **Fase 2C — Módulo `geo` y Catálogo Público:** geocodificación inversa cacheada, `GET /api/v1/public/complejos` (`ST_DWithin`/`ST_Distance`, degradación RN-19) y detalle por slug (RN-02).
- **Fase 3A — `reservas` (Jugador + Storage + Worker):** `LocalFileStorage` privado, disponibilidad/cotizador mixto RN-08/RN-09, Hold GIST (`23P01` -> `409`), comprobante manual (`POR_VALIDAR`), mis reservas/evidencia ACL y worker de expiración 30s (`FOR UPDATE SKIP LOCKED`).
- **Fase 3B — `reservas` (Dueño + Notificaciones/Push) y `reportes`:** agenda `/api/v1/dueno/reservas` con `alerta_demora` >30 min, aprobar/rechazar, externas/bloqueos GIST, lector QR, check-in/No-Show, cancelaciones/reprogramación, `/api/v1/notificaciones` + push y `GET /api/v1/dueno/reportes/resumen`.
- **Fase 3C — Cierre Operativo del Backend:** galería `/dueno/fotos`, foto de cancha, QR Yape/Plin y `GET /api/v1/public/media/*`; invalidación Valkey por slug; perfil/contraseña auth; worker CU-14 cada 5 min; Web Push VAPID opcional y CORS listo para Next.js.
- **Fase 4A.1 — Base Frontend Separada y Hero Cinemático (`/`):** Next.js 16 + `pnpm`, separación estricta `src/landing/` vs `src/webapp/` y Route Groups `(landing)` vs `(app)` (`/explorar`, `/login`), tipografía Bricolage Grotesque + Manrope + JetBrains Mono, activos `ffmpeg` `bt709` adaptativos (Desktop 1080p `WebM`/`MP4`/`WebP` + Móvil 9:16 `540x960` `WebM`/`MP4`/`WebP`, `balon-desarme.mp4` y logo `15 KB`), caché inmutable y Hero con despegue `0->2.4s`, contador `1->100` y 3 actos por scroll (GSAP + Lenis aislados en `src/landing/`).

## Siguiente Paso Exacto (En Chat Nuevo)

1. Iniciar **Fase 4A.2**: Sección blanca (`#FFFFFF`) de ingeniería y experiencia con `balon-desarme.mp4` controlado por scroll.
2. Continuar con **Fase 4A.3** (cierre comercial B2B/B2C y footer de Landing) antes de pasar a la WebApp operativa en **Fase 4B**.
