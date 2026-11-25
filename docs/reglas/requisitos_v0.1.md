# Síntesis Operativa de Requisitos — SRS v0.1

Fuente de verdad: `docs/srs/SRS - APK Móvil Fútbol - Digibyte Perú (v0.1).pdf` (SaaS B2B2C multi-inquilino con despliegue inicial en Huacho y escalable a otras ciudades).

## 1. Roles y 5 Pilares de Autogestión

| Rol | Acceso | Responsabilidades clave |
|---|---|---|
| **Super Administrador** (Digibyte Perú) | Panel central | Alta de complejos, planes, suscripciones (`PRUEBA`, `ACTIVA`, `VENCIDA`, `SUSPENDIDA`) y métricas globales. No interviene en fondos de reservas. |
| **Cliente SaaS / Dueño** (Tenant) | Suscripción mensual o anual | Autogestiona perfil, canchas, ubicación por coordenadas, horarios, tarifas, promociones, términos, medios de cobro, validación manual, reservas externas, *check-in* y reportes. |
| **Usuario Final / Jugador** | Gratuito (móvil/PWA) | Explora complejos cercanos, consulta disponibilidad en vivo, reserva y paga adelanto directo, recibe QR y sigue cercanía/ETA al complejo. |

**5 Pilares del Dueño:** (1) Perfil, infraestructura y ubicación por coordenadas; (2) Horarios y tarifas dinámicas por franja; (3) Términos, adelanto y cancelación propios; (4) Métodos de cobro propios (Yape/Plin, banco o pasarela directa); (5) Promociones y horas muertas.

## 2. Criterios Críticos No Negociables

- **Privacidad y opcionalidad GPS (RNF-04, RN-19):** El GPS del jugador es opcional (degradación a búsqueda por zona). Su posición en vivo se usa exclusivamente en sesión cliente para mapa/ruta/ETA/radio QR: **nunca se guarda en BD, logs ni caché, ni se expone al complejo o terceros**.
- **Flujo financiero directo ante SUNAT (RN-10, RN-14):** La plataforma no custodia ni recauda dinero de reservas ni emite facturación electrónica a nombre de complejos. Todo adelanto, saldo y eventual reembolso ocurre directamente entre jugador y complejo.

## 3. Requisitos Funcionales (RF-01 a RF-16)

| ID | Nombre | Prio | Aterrizaje Técnico (Go · PostgreSQL/PostGIS · Valkey · Next.js) |
|---|---|---|---|
| **RF-01** | Gestión de complejos y suscripciones | Alta | Go `admin`: CRUD `complejos`/`suscripciones`; cambio de estado impacta filtro público sin tocar reservas `CONFIRMADA`. |
| **RF-02** | Métricas globales SaaS | Media | Go `admin`: consultas agregadas SQL (activos, pruebas/por vencer, suspendidos, volumen de reservas) con caché corto en Valkey. |
| **RF-03** | Perfil e infraestructura | Alta | Go `complejos`: CRUD perfil, servicios extra, galería (URLs S3/R2/MinIO) y tabla `canchas` (6v6, 7v7, etc.) aislada por `complejo_id`. |
| **RF-04** | Ubicación guiada por coordenadas | Alta | Next.js: `navigator.geolocation` + pin ajustable MapLibre; Go llama geocodificación inversa (MapTiler/OSM) cacheada en Valkey; PG guarda `GEOGRAPHY(Point,4326)`, dirección editable y referencia (`<=150` chars). |
| **RF-05** | Horarios y tarifas dinámicas | Alta | PG: horarios por día y franjas tarifarias sin solapamiento por cancha; motor tarifario en Go prorratea bloques cuando una reserva cruza franjas. |
| **RF-06** | Términos y reglas de reserva | Alta | PG: % adelanto (`1–100%`) o monto fijo (HU-04), unidad mínima (`60m`/`30m`), ventana cancelación/reprogramación y texto legal; gate obligatorio para publicar. |
| **RF-07** | Métodos de cobro propios | Alta | PG: medios directos del tenant (Yape/Plin, cuentas bancarias, credenciales encriptadas de pasarela propia Culqi/MercadoPago). |
| **RF-08** | Promociones y horas muertas | Media | Go `tarifas`: reglas por día/franja y cupones con bandera de prioridad/no acumulación sobre hora muerta. |
| **RF-09** | Exploración de complejos cercanos | Alta | PG/PostGIS: consulta `ST_DWithin` / `ST_Distance` sobre complejos publicados y activos; Next.js renderiza pines MapLibre con distancia, dirección, referencias y tarifa base. |
| **RF-10** | Disponibilidad en vivo y Hold | Alta | Go + PG: lectura de slots libres (cacheada en Valkey con invalidación en escritura); al iniciar checkout se inserta `PENDIENTE_PAGO` con `EXCLUDE USING gist` + `expira_en` (10–15 min). |
| **RF-11** | Pago y aceptación de términos | Alta | Next.js exige checkbox de términos (timestamp guardado en PG); bifurcación: webhook/API pasarela del dueño o subida de captura a S3 + nro. operación. |
| **RF-12** | Validación manual de comprobantes | Alta | Go `pagos`: bandeja del dueño con evidencia, nro. operación y monto esperado; acción 1-clic aprobar (`CONFIRMADA`) o rechazar con motivo (`RECHAZADA`). |
| **RF-13** | Comprobante QR y guía de llegada | Alta | Go genera token QR firmado por reserva confirmada; Next.js muestra QR, saldo pendiente, mapa en vivo (MapLibre + OSRM coche/pie, solo en cliente) y enlace a Google Maps/Waze. |
| **RF-14** | Reservas externas y *check-in* | Alta | Go `reservas`: alta manual (WhatsApp/puerta) y bloqueos de mantenimiento (misma restricción `gist`); escaneo/búsqueda QR para *check-in* y liquidación de saldo (`COMPLETADA`). |
| **RF-15** | Cancelaciones, reprogramación y No-Show | Media | Go valida ventana del complejo antes de pasar a `CANCELADA` o reprogramar; dueño marca `NO_SHOW` en reservas confirmadas no asistidas. |
| **RF-16** | Reportes operativos y de ingresos | Media | PG/Go: reportes por rango de fechas filtrados por `complejo_id` (ingresos confirmados, adelantos vs. saldos, ocupación por cancha, cancelaciones). |

## 4. Requisitos No Funcionales (RNF-01 a RNF-08)

| ID | Categoría | Implementación en la Arquitectura |
|---|---|---|
| **RNF-01** | Seguridad y Aislamiento | JWT/sesiones en Valkey, RBAC en middleware Go y filtro obligatorio por `complejo_id` en toda consulta tenant. |
| **RNF-02** | Concurrencia e Integridad | Constraint `EXCLUDE USING gist (cancha_id WITH =, rango_horario WITH &&) WHERE (estado IN ('PENDIENTE_PAGO','POR_VALIDAR','CONFIRMADA'))` + worker Go de expiración. |
| **RNF-03** | Usabilidad Móvil | Next.js PWA mobile-first con acciones rápidas táctiles y carga ligera para jugador y dueño. |
| **RNF-04** | Privacidad de Ubicación | Posición GPS del jugador procesada solo en memoria del navegador (y llamadas efímeras de ruta sin persistir ni asociar a usuario/complejo). |
| **RNF-05** | Precisión Geográfica | PostGIS SRID 4326 con precisión de puerta de ingreso + fallback sin GPS por búsqueda de zona/distrito. |
| **RNF-06** | Rendimiento | Go compilado + `pgx/v5` pool + caché Valkey de disponibilidad/mapas + índices GIST/B-Tree en PG. |
| **RNF-07** | Disponibilidad | Contenedores Docker con reinicio automático, Caddy HTTPS, reservas confirmadas accesibles incluso ante picos nocturnos/fines de semana. |
| **RNF-08** | Escalabilidad Funcional | Modelo multi-inquilino parametrizable por complejidad/localidad sin código específico por ciudad. |

## 5. Reglas de Negocio (RN-01 a RN-20)

| ID | Regla | Aterrizaje Técnico |
|---|---|---|
| **RN-01** | Aislamiento estricto por complejo | `complejo_id` del token del tenant inyectado en contexto Go y cláusulas `WHERE`. |
| **RN-02** | Suspensión sin afectar confirmadas | Filtro público exige suscripción `PRUEBA`/`ACTIVA`; consultas de reservas ya `CONFIRMADA` permanecen operativas. |
| **RN-03** | Condiciones de publicación | Validación en Go antes de `publicado = true`: canchas activas, horarios, tarifas, coordenadas, términos y `>=1` medio de cobro. |
| **RN-04** | Hold anticolisión (10–15 min) | `PENDIENTE_PAGO` con `expira_en` en PG + clave TTL en Valkey y ticker Go que transiciona a `EXPIRADA`. |
| **RN-05** | Unicidad de horario por cancha | Constraint `EXCLUDE USING gist` sobre `tstzrange` en PostgreSQL (bloqueo atómico real). |
| **RN-06** | Bloqueos manuales en tiempo real | Reservas externas y mantenimientos ocupan `rango_horario` en la misma estructura e invalidan caché de disponibilidad. |
| **RN-07** | Unidad mínima configurable | `unidad_minima_min` por complejo (`60` estándar, `30` opcional) validada en generador de slots y API. |
| **RN-08** | Franja tarifaria mixta | Motor de precios en Go parte el intervalo en sub-bloques y suma tarifa proporcional exacta de cada franja. |
| **RN-09** | No acumulación de descuentos | Bandera `permite_acumular` (default `false`): cupones no se aplican sobre tarifa de hora muerta salvo autorización del dueño. |
| **RN-10** | Flujo directo de dinero | Cero pasarela central del SaaS; pagos van directo a cuentas/pasarela del tenant. |
| **RN-11** | Bifurcación de confirmación | Webhook válido de pasarela -> `CONFIRMADA`; comprobante manual -> `POR_VALIDAR`. |
| **RN-12** | Saldo pendiente | PG persiste `monto_total`, `monto_adelanto` y `saldo_pendiente = total - adelanto` hasta liquidarse en *check-in*. |
| **RN-13** | Ventana de cancelación | Go compara `now() <= inicio - ventana_cancelacion_horas` antes de permitir cancelación/reprogramación del jugador. |
| **RN-14** | Reembolso directo | Cambio a `CANCELADA` registra indicador de devolución pendiente; el extorno lo ejecuta el complejo fuera de la plataforma. |
| **RN-15** | Recomendación de coordenadas | UI de onboarding del dueño destaca botón de geolocalización de navegador + pin en puerta de entrada. |
| **RN-16** | Geocodificación inversa y referencias | Coordenadas -> dirección editable vía servicio geo (cacheado en Valkey) + `referencia VARCHAR(150)`. |
| **RN-17** | Compatibilidad cartográfica | Coordenadas WGS84 (`lat`, `lng`) alimentan MapLibre y deep-links `geo:` / Google Maps / Waze. |
| **RN-18** | Distancia y ETA por modo | OSRM (`driving` y `foot`) consultado según selector vehículo/a pie del jugador. |
| **RN-19** | Degradación sin GPS y privacidad | Permiso denegado habilita búsqueda por zona; posición del jugador jamás se persiste ni comparte. |
| **RN-20** | Prioridad de QR al llegar | Cálculo local en cliente (`Haversine < 80 m`) eleva/destaca tarjeta del QR automáticamente. |

## 6. Máquina de 8 Estados de Reserva

| Estado | Significado Operativo | Efecto en Horario (GIST) | Transiciones Principales |
|---|---|---|---|
| `PENDIENTE_PAGO` | Hold temporal activo (10–15 min) mientras el jugador paga | **Bloqueado temporalmente** | -> `POR_VALIDAR`, `CONFIRMADA`, `EXPIRADA` |
| `POR_VALIDAR` | Comprobante manual cargado; espera revisión del dueño | **Bloqueado** | -> `CONFIRMADA`, `RECHAZADA` |
| `CONFIRMADA` | Pago validado (pasarela o dueño); QR emitido | **Ocupado definitivamente** | -> `COMPLETADA`, `CANCELADA`, `NO_SHOW` |
| `COMPLETADA` | *Check-in* en cancha y saldo liquidado | Cerrado (Histórico) | Terminal |
| `EXPIRADA` | Venció Hold sin pago ni comprobante | Liberado automáticamente | Terminal |
| `RECHAZADA` | Dueño rechazó comprobante con motivo | Liberado | Terminal |
| `CANCELADA` | Anulada por jugador/dueño según política | Liberado | Terminal |
| `NO_SHOW` | Inasistencia a reserva confirmada (retiene adelanto) | Cerrado (Histórico) | Terminal |

## 7. Casos de Uso (CU-01..CU-14) y Trazabilidad HU-01..HU-10

| ID | Actor | Caso de Uso | HU Asociada |
|---|---|---|---|
| **CU-01** | Super Admin | Gestionar complejos y suscripciones (alta, planes, vigencia, suspender/reactivar) | HU-01 |
| **CU-02** | Super Admin | Consultar métricas globales del SaaS | — |
| **CU-03** | Dueño | Configurar perfil, galería, servicios extra y catálogo de canchas | — |
| **CU-04** | Dueño | Configurar ubicación exacta por coordenadas, dirección automática y referencias | HU-02 |
| **CU-05** | Dueño | Configurar horarios semanales, tarifas por franja y promociones/horas muertas | HU-03 |
| **CU-06** | Dueño | Configurar términos, adelanto, políticas de cancelación y medios de cobro propios | HU-04 |
| **CU-07** | Dueño | Validar pagos manuales (aprobar con emisión QR o rechazar con motivo) | HU-09 |
| **CU-08** | Dueño | Gestionar agenda, reservas externas (WhatsApp/puerta), bloqueos, *check-in* y saldo | HU-10 |
| **CU-09** | Dueño | Consultar reportes de ingresos, adelantos/saldos, ocupación y cancelaciones | — |
| **CU-10** | Jugador | Explorar complejos cercanos en mapa con distancia, dirección, referencias y tarifa | HU-05 |
| **CU-11** | Jugador | Reservar cancha, activar Hold, aceptar términos y pagar adelanto / subir comprobante | HU-06, HU-07 |
| **CU-12** | Jugador | Seguir cercanía, ruta y ETA (vehículo/pie) con enlace a mapas externos y QR a `<80m` | HU-08 |
| **CU-13** | Jugador | Gestionar mis reservas (historial, vigentes, QR, cancelar/reprogramar en ventana) | — |
| **CU-14** | Sistema | Expirar Holds (`EXPIRADA`), enviar recordatorios previos y alertas de suscripción | HU-06 |

## Decisiones de Aclaración (Base Fase 1)

1. **Adelanto:** soportar `PORCENTAJE` y `MONTO_FIJO` en el esquema SQL.
2. **Creación del Hold (`PENDIENTE_PAGO`):** se crea al confirmar *"Aceptar términos y continuar al pago"* (Flujo 7.1), nunca antes.
3. **`POR_VALIDAR`:** no expira automáticamente para proteger pagos ya realizados; permanece bloqueado hasta aprobación/rechazo del dueño con alerta visual si supera 30 min sin revisión.
4. **Reprogramación:** se mantienen los 8 estados exactos; reprogramar actualiza rango/cancha en la misma reserva, registra auditoría y recalcula `saldo_pendiente` conservando el adelanto pagado.
5. **Autenticación del jugador:** exploración y consulta de horarios 100% pública/anónima; registro/login al reservar con correo, contraseña, nombre y celular (WhatsApp).
6. **Notificaciones MVP:** bandeja in-app + Web Push PWA + correo electrónico de respaldo (sin SMS en MVP).
7. **Catálogo de planes SaaS (amplía RF-01 y RF-02):** el Super Admin gestiona planes configurables (`nombre`, `periodicidad`: `MENSUAL` | `ANUAL` | `PRUEBA`, `duración`, `precio`, `activo`) asignables a complejos, con trazabilidad de pagos y renovaciones de suscripción.
