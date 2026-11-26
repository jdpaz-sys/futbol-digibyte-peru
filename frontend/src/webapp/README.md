# Espacio Operativo WebApp (`src/webapp/`)

Dominio estrictamente separado de `src/landing/` y asociado al Route Group `src/app/(app)/`.

## Alcance reservado para Fase 4B
- **Jugador (Mobile-First):** catálogo geolocalizado (`/explorar`), ficha pública por complejo, congelamiento de horario en tiempo real (`Hold`), carga de comprobante Yape/Plin y billetera de tickets QR.
- **Dueño de Complejo:** agenda operativa con alertas de validación, aprobación/rechazo en un clic, check-in QR en puerta y resumen de ingresos.
- **Super Admin:** gestión B2B de suscripciones, altas de complejos y métricas globales.
- **Consumo directo de API:** comunicación directa del navegador contra `NEXT_PUBLIC_API_URL` (`/api/v1/*` en Go) sin rutas proxy en `src/app/api/`.
