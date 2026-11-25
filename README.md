# Fútbol Digibyte Perú

Plataforma SaaS B2B2C multi-inquilino para la gestión de complejos deportivos y reserva móvil en tiempo real, desarrollada para **Digibyte Perú**.

## Estado Actual

- **Fase 0 — Planificación y Preparación del Repositorio:** completada (base documental versionada y SRS aprobado).
- **Siguiente paso:** Fase 1 (`infra/docker-compose.dev.yml`, migraciones SQL con PostGIS/GIST y núcleo Backend en Go).

## Stack Definido

- **Frontend:** Next.js (App Router, TypeScript, PWA, `pnpm`), Tailwind CSS, Radix/shadcn, Motion, Lucide, MapLibre GL JS / MapTiler / OSRM.
- **Backend:** Go (monolito modular plano con `pgx/v5`).
- **Datos y Caché:** PostgreSQL 16 + PostGIS, Valkey, Storage S3 compatible (Cloudflare R2 / MinIO).
- **Red e Infra:** Docker Compose + Caddy (`/api/v1/*` -> Go, `/*` -> Next.js).

## Estructura de Documentación (`docs/`)

```text
docs/
├── arquitectura_v0.2.md                                    # Contrato técnico definitivo y hoja de ruta (Fases 1–6)
├── estado_v0.1.md                                          # Bitácora viva de fase actual y siguiente paso
├── reglas/
│   ├── agente_v0.1.md                                      # Reglas operativas de desarrollo, diseño, Git y QA
│   └── requisitos_v0.1.md                                  # Síntesis operativa del SRS (RF, RNF, RN, estados y CU)
└── srs/
    └── SRS - APK Móvil Fútbol - Digibyte Perú (v0.1).pdf   # Especificación funcional aprobada
```
