# Contrato Técnico de Arquitectura — Fútbol Digibyte Perú (v0.2)

Contrato definitivo que traduce el SRS v0.1 en una arquitectura desacoplada, plana y operable en producción con bajo costo operativo.

## 1. Stack Tecnológico Acordado

| Capa | Tecnología | Rol Principal |
|---|---|---|
| **Edge / Reverse Proxy** | **Caddy** | Terminación HTTPS automática y enrutamiento: `/api/v1/*` -> Go, `/*` -> Next.js. |
| **Frontend** | **Next.js** (App Router, TypeScript, PWA, `pnpm`) | UI móvil del jugador, panel de autogestión del dueño y panel Super Admin. |
| **UI y Mapas** | **Tailwind CSS + Radix/shadcn + Motion + Lucide**; **MapLibre GL JS + MapTiler + OSRM** | Paleta Digibyte (`#0F172A`, `#2563EB`, blanco, pizarra; cero morado/violeta/índigo), mapa interactivo y cálculo de ruta/ETA en vehículo o a pie. |
| **Backend** | **Go** (monolito modular plano con `pgx/v5`) | API REST `/api/v1/*`, lógica de dominio, motor tarifario, worker de expiración de Holds y webhooks de pasarelas propias. |
| **Base de Datos** | **PostgreSQL 16 + PostGIS** | Fuente única de verdad transaccional, multi-tenancy por `complejo_id`, anticolisión GIST y consultas geoespaciales. |
| **Caché / Estado Efímero** | **Valkey** | Caché de disponibilidad y geocodificación inversa, sesiones/refresh tokens, rate limiting y apoyo TTL al Hold. |
| **Almacenamiento Objetos** | **S3 Compatible** (Cloudflare R2 en prod / MinIO en local) | Galería de fotos de complejos/canchas y comprobantes manuales (Yape/Plin/transferencias) mediante URLs prefirmadas. |
| **Infraestructura** | **Docker Compose** | Orquestación reproducible en desarrollo (`infra/docker-compose.dev.yml`) y despliegue productivo. |

## 2. Flujo de Red y Desacoplamiento Real

```text
[Navegador / PWA] --HTTPS--> [Caddy]
                               ├── /api/v1/*  --> [Go API :8080] --> [PostgreSQL 16 + PostGIS]
                               │                       ├──> [Valkey :6379]
                               │                       └──> [MinIO / Cloudflare R2]
                               └── /*         --> [Next.js :3000]
```

- **Regla estricta:** Next.js no expone proxies en `app/api/*` hacia Go. Toda mutación y consulta de dominio se dirige a `/api/v1/*` a través de Caddy.
- **Privacidad GPS:** Las coordenadas en vivo del jugador permanecen en el navegador. El cálculo de cercanía (`<80 m` para destacar QR) es local y las peticiones de ruta/ETA a OSRM no persisten historial ni vinculan identidad al complejo.
- **Pagos directos (SUNAT):** Go nunca procesa recaudación centralizada; almacena configuración de cobro del tenant (Yape/Plin/banco o credenciales de pasarela propia) y solo gestiona evidencia/estado de reserva.

## 3. Estructura Plana Acordada (Fases 1+)

*Nota: estas carpetas se crearán progresivamente a partir de la Fase 1; no se crean vacías en Fase 0.*

```text
backend/
├── cmd/api/            # Entrypoint del servidor HTTP y worker de expiración
├── migrations/         # Migraciones SQL numeradas (DDL, índices, GIST, PostGIS)
└── internal/
    ├── db/             # Pool pgx/v5, transacciones y helpers SQL
    ├── http/           # Router, middlewares (auth, RBAC, tenant, rate limit)
    └── modules/        # Módulos planos por dominio (admin, auth, complejos, tarifas, reservas, pagos, reportes)

frontend/
├── public/             # Manifest PWA, íconos y assets estáticos
└── src/
    ├── app/            # Rutas App Router (jugador, dueño, admin)
    ├── components/     # Componentes UI (Radix/shadcn, mapa, calendario, QR)
    └── lib/            # Cliente API (/api/v1), tipos, helpers de formato y geo cliente
```

## 4. Rol Exacto de PostgreSQL/PostGIS vs. Valkey

### PostgreSQL 16 + PostGIS (Fuente de Verdad)
- **Aislamiento multi-inquilino:** Toda tabla de negocio del complejo lleva `complejo_id NOT NULL` e índices compuestos; el middleware Go inyecta el `complejo_id` autenticado.
- **Anticolisión atómica (RN-04, RN-05, RN-06):** Extensión `btree_gist` + restricción:
  `EXCLUDE USING gist (cancha_id WITH =, rango_horario WITH &&) WHERE (estado IN ('PENDIENTE_PAGO', 'POR_VALIDAR', 'CONFIRMADA'))`
  (aplica tanto a reservas web como externas y bloqueos de mantenimiento).
- **Cercanía geográfica (RF-04, RF-09, RN-15..RN-17):** Columna `ubicacion GEOGRAPHY(Point, 4326)` con índice `GIST` para `ST_DWithin` y `ST_Distance`.
- **Integridad de estados y finanzas:** Máquina de 8 estados con transiciones validadas en transacción, registro de aceptación de términos y cálculo persistido de `monto_total`, `monto_adelanto` y `saldo_pendiente`.

### Valkey (Aceleración y Control Operativo)
- **Caché de disponibilidad:** Clave por `disp:{cancha_id}:{fecha}` con TTL corto e invalidación inmediata al crear/modificar/expirar reservas o bloqueos.
- **Caché de geocodificación inversa:** Clave por coordenada redondeada (`geo:rev:{lat5}:{lng5}`) para reducir latencia y cuota externa al configurar complejos.
- **Sesiones y seguridad:** Almacenamiento de refresh tokens/sesiones revocables y **rate limiting** por IP/usuario (protección de Holds, login y subida de comprobantes).
- **Apoyo TTL al Hold:** Claves `hold:{reserva_id}` con expiración sincronizada a `expira_en` como acelerador de lectura/notificación; un worker periódico en Go contra PostgreSQL garantiza que ningún Hold vencido quede colgado aunque Valkey reinicie.

## 5. Hoja de Ruta (Fases 1 a 6)

| Fase | Alcance Principal | Entregable Verificable |
|---|---|---|
| **Fase 1** | Entorno local y núcleo Backend | `infra/docker-compose.dev.yml` (PG+PostGIS, Valkey, MinIO, Caddy), migraciones SQL base (esquema + GIST) y núcleo Go (`health`, auth, tenant). |
| **Fase 2** | Super Admin y Onboarding del Dueño | Módulos `admin`, `complejos` (coordenadas + geo inversa + S3), `tarifas` (franjas, horas muertas, cupones), términos y medios de cobro (RF-01..RF-08). |
| **Fase 3** | Motor de Reservas, Hold y Pagos | Disponibilidad en vivo, Hold anticolisión, worker de expiración, validación manual 1-clic, webhook pasarela propia, QR, externas, *check-in* y reportes (RF-10..RF-16). |
| **Fase 4** | Frontend PWA Jugador y Mapas | Next.js (`pnpm`), mapa cercano MapLibre, flujo de reserva con temporizador Hold, pago/comprobante, vista QR y seguimiento ruta/ETA OSRM con privacidad GPS. |
| **Fase 5** | Paneles Dueño y Super Admin | Agenda diaria, bandeja de validación manual rápida, onboarding guiado de coordenadas, reportes operativos y control de suscripciones SaaS. |
| **Fase 6** | Endurecimiento y Despliegue MVP | Pruebas de concurrencia (doble reserva simultánea), revisión de seguridad/aislamiento, Caddy HTTPS productivo y despliegue piloto (Huacho). |
