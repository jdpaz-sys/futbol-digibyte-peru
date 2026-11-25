-- ============================================================================
-- Fútbol Digibyte Perú — Migración Inicial 000001_init (UP)
-- Motor: PostgreSQL 16 + PostGIS 3.4 + btree_gist + pgcrypto
-- Regla crítica RN-19 / RNF-04: CERO tablas o columnas de ubicación del jugador.
-- ============================================================================

BEGIN;

-- 1. Extensiones requeridas
CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS postgis;
CREATE EXTENSION IF NOT EXISTS btree_gist;

-- 2. Función utilitaria para mantener updated_at al día
CREATE OR REPLACE FUNCTION fn_set_updated_at()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$;

-- ============================================================================
-- TABLA 1: usuarios
-- Roles: SUPER_ADMIN (Digibyte Perú), DUENO (Tenant), JUGADOR (Cliente móvil)
-- ============================================================================
CREATE TABLE usuarios (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    rol                 VARCHAR(20) NOT NULL,
    email               VARCHAR(255) NOT NULL,
    password_hash       VARCHAR(255) NOT NULL,
    nombre_completo     VARCHAR(150) NOT NULL,
    telefono            VARCHAR(20),
    activo              BOOLEAN NOT NULL DEFAULT true,
    email_verificado_at TIMESTAMPTZ,
    ultimo_login_at     TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT chk_usuarios_rol CHECK (rol IN ('SUPER_ADMIN', 'DUENO', 'JUGADOR')),
    CONSTRAINT chk_usuarios_email_no_vacio CHECK (btrim(email) <> ''),
    CONSTRAINT chk_usuarios_nombre_no_vacio CHECK (btrim(nombre_completo) <> ''),
    CONSTRAINT chk_usuarios_telefono_requerido CHECK (
        rol = 'SUPER_ADMIN' OR (telefono IS NOT NULL AND btrim(telefono) <> '')
    )
);

CREATE UNIQUE INDEX uq_usuarios_email_lower ON usuarios (LOWER(email));
CREATE INDEX idx_usuarios_rol_activo ON usuarios (rol, activo);

CREATE TRIGGER trg_usuarios_updated_at
    BEFORE UPDATE ON usuarios
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

-- ============================================================================
-- TABLA 2: planes_suscripcion
-- Catálogo SaaS gestionado por Super Admin (MENSUAL, ANUAL, PRUEBA)
-- ============================================================================
CREATE TABLE planes_suscripcion (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    codigo            VARCHAR(40) NOT NULL UNIQUE,
    nombre            VARCHAR(100) NOT NULL,
    periodicidad      VARCHAR(20) NOT NULL,
    unidad_duracion   VARCHAR(10) NOT NULL,
    cantidad_duracion SMALLINT NOT NULL,
    precio            NUMERIC(10,2) NOT NULL DEFAULT 0.00,
    descripcion       TEXT,
    activo            BOOLEAN NOT NULL DEFAULT true,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT chk_planes_codigo_upper CHECK (codigo = UPPER(codigo) AND btrim(codigo) <> ''),
    CONSTRAINT chk_planes_periodicidad CHECK (periodicidad IN ('MENSUAL', 'ANUAL', 'PRUEBA')),
    CONSTRAINT chk_planes_unidad_duracion CHECK (unidad_duracion IN ('DIAS', 'MESES')),
    CONSTRAINT chk_planes_cantidad_positiva CHECK (cantidad_duracion > 0),
    CONSTRAINT chk_planes_precio_coherente CHECK (
        (periodicidad = 'PRUEBA' AND precio = 0)
        OR (periodicidad IN ('MENSUAL', 'ANUAL') AND precio >= 0)
    )
);

CREATE TRIGGER trg_planes_suscripcion_updated_at
    BEFORE UPDATE ON planes_suscripcion
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

-- ============================================================================
-- TABLA 3: complejos
-- Entidad raíz multi-tenant (aislamiento por complejo_id) + PostGIS WGS84
-- ============================================================================
CREATE TABLE complejos (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    dueno_id              UUID NOT NULL REFERENCES usuarios(id) ON DELETE RESTRICT,
    plan_actual_id        UUID REFERENCES planes_suscripcion(id) ON DELETE RESTRICT,
    slug                  VARCHAR(120) NOT NULL UNIQUE,
    nombre                VARCHAR(150) NOT NULL,
    descripcion           TEXT,
    telefono_contacto     VARCHAR(20),
    servicios             JSONB NOT NULL DEFAULT '[]'::jsonb,
    departamento          VARCHAR(60) NOT NULL DEFAULT 'Lima',
    provincia             VARCHAR(60) NOT NULL DEFAULT 'Huaura',
    distrito              VARCHAR(80) NOT NULL DEFAULT 'Huacho',
    ubicacion             GEOGRAPHY(Point, 4326),
    direccion_escrita     VARCHAR(255),
    referencias           VARCHAR(150),
    publicado             BOOLEAN NOT NULL DEFAULT false,
    unidad_reserva_min    SMALLINT NOT NULL DEFAULT 60,
    hold_duracion_min     SMALLINT NOT NULL DEFAULT 10,
    tipo_adelanto         VARCHAR(20) NOT NULL DEFAULT 'PORCENTAJE',
    valor_adelanto        NUMERIC(10,2) NOT NULL DEFAULT 50.00,
    horas_min_cancelacion SMALLINT NOT NULL DEFAULT 24,
    terminos_reserva      TEXT,
    estado_suscripcion    VARCHAR(20) NOT NULL DEFAULT 'PRUEBA',
    suscripcion_vence_at  TIMESTAMPTZ,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT chk_complejos_slug_formato CHECK (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
    CONSTRAINT chk_complejos_nombre_no_vacio CHECK (btrim(nombre) <> ''),
    CONSTRAINT chk_complejos_servicios_array CHECK (jsonb_typeof(servicios) = 'array'),
    CONSTRAINT chk_complejos_unidad_reserva CHECK (unidad_reserva_min IN (30, 60)),
    CONSTRAINT chk_complejos_hold_duracion CHECK (hold_duracion_min BETWEEN 5 AND 30),
    CONSTRAINT chk_complejos_tipo_adelanto CHECK (tipo_adelanto IN ('PORCENTAJE', 'MONTO_FIJO')),
    CONSTRAINT chk_complejos_adelanto_valido CHECK (
        (tipo_adelanto = 'PORCENTAJE' AND valor_adelanto BETWEEN 1 AND 100)
        OR (tipo_adelanto = 'MONTO_FIJO' AND valor_adelanto > 0)
    ),
    CONSTRAINT chk_complejos_ventana_cancelacion CHECK (horas_min_cancelacion BETWEEN 0 AND 168),
    CONSTRAINT chk_complejos_estado_suscripcion CHECK (
        estado_suscripcion IN ('PRUEBA', 'ACTIVA', 'VENCIDA', 'SUSPENDIDA')
    ),
    CONSTRAINT chk_complejos_publicacion_requisitos CHECK (
        NOT publicado OR (
            ubicacion IS NOT NULL
            AND direccion_escrita IS NOT NULL AND btrim(direccion_escrita) <> ''
            AND terminos_reserva IS NOT NULL AND btrim(terminos_reserva) <> ''
            AND (
                (tipo_adelanto = 'PORCENTAJE' AND valor_adelanto BETWEEN 1 AND 100)
                OR (tipo_adelanto = 'MONTO_FIJO' AND valor_adelanto > 0)
            )
        )
    )
);

CREATE INDEX idx_complejos_ubicacion_gist ON complejos USING gist (ubicacion);
CREATE INDEX idx_complejos_zona_publicos ON complejos (departamento, provincia, distrito)
    WHERE publicado = true AND estado_suscripcion IN ('PRUEBA', 'ACTIVA');
CREATE INDEX idx_complejos_dueno ON complejos (dueno_id);
CREATE INDEX idx_complejos_suscripcion ON complejos (estado_suscripcion, suscripcion_vence_at);

CREATE TRIGGER trg_complejos_updated_at
    BEFORE UPDATE ON complejos
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

-- ============================================================================
-- TABLA 4: complejo_fotos
-- Galería del complejo con portada única parcial y clave de almacenamiento S3/R2
-- ============================================================================
CREATE TABLE complejo_fotos (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    complejo_id UUID NOT NULL REFERENCES complejos(id) ON DELETE CASCADE,
    url         TEXT NOT NULL,
    storage_key TEXT,
    descripcion VARCHAR(150),
    orden       SMALLINT NOT NULL DEFAULT 0,
    es_portada  BOOLEAN NOT NULL DEFAULT false,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT chk_complejo_fotos_orden CHECK (orden >= 0)
);

CREATE UNIQUE INDEX uq_complejo_fotos_portada ON complejo_fotos (complejo_id)
    WHERE es_portada = true;
CREATE INDEX idx_complejo_fotos_orden ON complejo_fotos (complejo_id, orden);

-- ============================================================================
-- TABLA 5: suscripciones_historial
-- Trazabilidad comercial y renovaciones SaaS registradas por Super Admin
-- ============================================================================
CREATE TABLE suscripciones_historial (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    complejo_id      UUID NOT NULL REFERENCES complejos(id) ON DELETE CASCADE,
    plan_id          UUID NOT NULL REFERENCES planes_suscripcion(id) ON DELETE RESTRICT,
    plan_nombre      VARCHAR(100) NOT NULL,
    periodicidad     VARCHAR(20) NOT NULL,
    monto_cobrado    NUMERIC(10,2) NOT NULL DEFAULT 0.00,
    metodo_pago_saas VARCHAR(30),
    referencia_pago  VARCHAR(100),
    estado           VARCHAR(20) NOT NULL,
    inicio_at        TIMESTAMPTZ NOT NULL,
    fin_at           TIMESTAMPTZ NOT NULL,
    registrado_por   UUID NOT NULL REFERENCES usuarios(id) ON DELETE RESTRICT,
    observaciones    TEXT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT chk_suscripciones_hist_periodicidad CHECK (periodicidad IN ('MENSUAL', 'ANUAL', 'PRUEBA')),
    CONSTRAINT chk_suscripciones_hist_monto CHECK (monto_cobrado >= 0),
    CONSTRAINT chk_suscripciones_hist_estado CHECK (
        estado IN ('PRUEBA', 'ACTIVA', 'VENCIDA', 'SUSPENDIDA', 'CANCELADA')
    ),
    CONSTRAINT chk_suscripciones_hist_fechas CHECK (inicio_at < fin_at)
);

CREATE INDEX idx_suscripciones_hist_complejo ON suscripciones_historial (complejo_id, inicio_at DESC);

-- ============================================================================
-- TABLA 6: metodos_pago_complejo
-- Medios de cobro directos del tenant (sin intermediación financiera del SaaS)
-- ============================================================================
CREATE TABLE metodos_pago_complejo (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    complejo_id           UUID NOT NULL REFERENCES complejos(id) ON DELETE CASCADE,
    tipo                  VARCHAR(20) NOT NULL,
    etiqueta              VARCHAR(80),
    titular               VARCHAR(150),
    entidad_financiera    VARCHAR(60),
    numero_destino        VARCHAR(50),
    cci                   VARCHAR(25),
    qr_url                TEXT,
    qr_storage_key        TEXT,
    llave_publica         VARCHAR(200),
    credenciales_cifradas BYTEA,
    instrucciones         VARCHAR(255),
    activo                BOOLEAN NOT NULL DEFAULT true,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT uq_metodos_pago_id_complejo UNIQUE (id, complejo_id),
    CONSTRAINT chk_metodos_pago_tipo CHECK (
        tipo IN ('YAPE', 'PLIN', 'TRANSFERENCIA', 'CULQI', 'MERCADOPAGO')
    )
);

CREATE INDEX idx_metodos_pago_complejo_activos ON metodos_pago_complejo (complejo_id, activo);

CREATE TRIGGER trg_metodos_pago_complejo_updated_at
    BEFORE UPDATE ON metodos_pago_complejo
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

-- ============================================================================
-- TABLA 7: canchas
-- Infraestructura deportiva por complejo con clave compuesta para FKs seguras
-- ============================================================================
CREATE TABLE canchas (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    complejo_id      UUID NOT NULL REFERENCES complejos(id) ON DELETE CASCADE,
    nombre           VARCHAR(100) NOT NULL,
    modalidad        VARCHAR(15) NOT NULL,
    superficie       VARCHAR(25) NOT NULL DEFAULT 'SINTETICO',
    techada          BOOLEAN NOT NULL DEFAULT false,
    iluminacion      BOOLEAN NOT NULL DEFAULT true,
    foto_url         TEXT,
    foto_storage_key TEXT,
    orden            SMALLINT NOT NULL DEFAULT 0,
    activa           BOOLEAN NOT NULL DEFAULT true,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT uq_canchas_id_complejo UNIQUE (id, complejo_id),
    CONSTRAINT chk_canchas_no_nil_uuid CHECK (id <> '00000000-0000-0000-0000-000000000000'::uuid),
    CONSTRAINT chk_canchas_nombre_no_vacio CHECK (btrim(nombre) <> ''),
    CONSTRAINT chk_canchas_modalidad CHECK (
        modalidad IN ('5v5', '6v6', '7v7', '8v8', '9v9', '11v11')
    ),
    CONSTRAINT chk_canchas_superficie CHECK (
        superficie IN ('SINTETICO', 'GRASS_NATURAL', 'LOZA', 'PARQUET')
    ),
    CONSTRAINT chk_canchas_orden CHECK (orden >= 0)
);

CREATE INDEX idx_canchas_complejo_activas ON canchas (complejo_id, activa, orden);

CREATE TRIGGER trg_canchas_updated_at
    BEFORE UPDATE ON canchas
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

-- ============================================================================
-- TABLA 8: horarios_atencion
-- Horario semanal por complejo en minutos del día (0..1440; 0=Domingo..6=Sábado)
-- ============================================================================
CREATE TABLE horarios_atencion (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    complejo_id     UUID NOT NULL REFERENCES complejos(id) ON DELETE CASCADE,
    dia_semana      SMALLINT NOT NULL,
    minuto_apertura SMALLINT NOT NULL DEFAULT 480,
    minuto_cierre   SMALLINT NOT NULL DEFAULT 1380,
    cerrado         BOOLEAN NOT NULL DEFAULT false,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT uq_horarios_complejo_dia UNIQUE (complejo_id, dia_semana),
    CONSTRAINT chk_horarios_dia_semana CHECK (dia_semana BETWEEN 0 AND 6),
    CONSTRAINT chk_horarios_minutos_rango CHECK (
        minuto_apertura BETWEEN 0 AND 1440
        AND minuto_cierre BETWEEN 0 AND 1440
    ),
    CONSTRAINT chk_horarios_apertura_menor_cierre CHECK (
        cerrado OR minuto_apertura < minuto_cierre
    )
);

CREATE TRIGGER trg_horarios_atencion_updated_at
    BEFORE UPDATE ON horarios_atencion
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

-- ============================================================================
-- TABLA 9: tarifas_franja
-- Tarifas dinámicas generales (cancha_id IS NULL) o por cancha sin solapamiento
-- ============================================================================
CREATE TABLE tarifas_franja (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    complejo_id    UUID NOT NULL REFERENCES complejos(id) ON DELETE CASCADE,
    cancha_id      UUID,
    dia_semana     SMALLINT NOT NULL,
    minuto_inicio  SMALLINT NOT NULL,
    minuto_fin     SMALLINT NOT NULL,
    precio_hora    NUMERIC(10,2) NOT NULL,
    etiqueta       VARCHAR(80),
    es_hora_muerta BOOLEAN NOT NULL DEFAULT false,
    activa         BOOLEAN NOT NULL DEFAULT true,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT fk_tarifas_cancha_complejo
        FOREIGN KEY (cancha_id, complejo_id) REFERENCES canchas(id, complejo_id) ON DELETE CASCADE,
    CONSTRAINT chk_tarifas_cancha_no_nil_uuid CHECK (
        cancha_id IS NULL OR cancha_id <> '00000000-0000-0000-0000-000000000000'::uuid
    ),
    CONSTRAINT chk_tarifas_dia_semana CHECK (dia_semana BETWEEN 0 AND 6),
    CONSTRAINT chk_tarifas_minutos_validos CHECK (
        minuto_inicio >= 0
        AND minuto_fin <= 1440
        AND minuto_inicio < minuto_fin
    ),
    CONSTRAINT chk_tarifas_precio_positivo CHECK (precio_hora > 0),
    CONSTRAINT ex_tarifas_franja_no_solape EXCLUDE USING gist (
        complejo_id WITH =,
        (COALESCE(cancha_id, '00000000-0000-0000-0000-000000000000'::uuid)) WITH =,
        dia_semana WITH =,
        (int4range(minuto_inicio::integer, minuto_fin::integer, '[)')) WITH &&
    ) WHERE (activa = true)
);

CREATE INDEX idx_tarifas_complejo_dia ON tarifas_franja (complejo_id, dia_semana)
    WHERE activa = true;

CREATE TRIGGER trg_tarifas_franja_updated_at
    BEFORE UPDATE ON tarifas_franja
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

-- ============================================================================
-- TABLA 10: cupones
-- Promociones por complejo con límite atómico de usos y regla de hora muerta
-- ============================================================================
CREATE TABLE cupones (
    id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    complejo_id            UUID NOT NULL REFERENCES complejos(id) ON DELETE CASCADE,
    codigo                 VARCHAR(40) NOT NULL,
    descripcion            VARCHAR(150),
    tipo_descuento         VARCHAR(20) NOT NULL,
    valor_descuento        NUMERIC(10,2) NOT NULL,
    valido_desde           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    valido_hasta           TIMESTAMPTZ,
    max_usos               INTEGER,
    usos_actuales          INTEGER NOT NULL DEFAULT 0,
    acumulable_hora_muerta BOOLEAN NOT NULL DEFAULT false,
    activo                 BOOLEAN NOT NULL DEFAULT true,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT uq_cupones_id_complejo UNIQUE (id, complejo_id),
    CONSTRAINT uq_cupones_complejo_codigo UNIQUE (complejo_id, codigo),
    CONSTRAINT chk_cupones_codigo_upper CHECK (codigo = UPPER(codigo) AND btrim(codigo) <> ''),
    CONSTRAINT chk_cupones_tipo_descuento CHECK (tipo_descuento IN ('PORCENTAJE', 'MONTO_FIJO')),
    CONSTRAINT chk_cupones_valor_valido CHECK (
        (tipo_descuento = 'PORCENTAJE' AND valor_descuento > 0 AND valor_descuento <= 100)
        OR (tipo_descuento = 'MONTO_FIJO' AND valor_descuento > 0)
    ),
    CONSTRAINT chk_cupones_vigencia CHECK (valido_hasta IS NULL OR valido_desde < valido_hasta),
    CONSTRAINT chk_cupones_usos_validos CHECK (
        usos_actuales >= 0
        AND (max_usos IS NULL OR (max_usos > 0 AND usos_actuales <= max_usos))
    )
);

CREATE TRIGGER trg_cupones_updated_at
    BEFORE UPDATE ON cupones
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

-- ============================================================================
-- TABLA 11: reservas
-- Núcleo transaccional: 8 estados, rango [) estricto y bloqueo GIST anticolisión
-- ============================================================================
CREATE TABLE reservas (
    id                       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    complejo_id              UUID NOT NULL REFERENCES complejos(id) ON DELETE RESTRICT,
    cancha_id                UUID NOT NULL,
    jugador_id               UUID REFERENCES usuarios(id) ON DELETE RESTRICT,
    creado_por               UUID REFERENCES usuarios(id) ON DELETE RESTRICT,
    origen                   VARCHAR(25) NOT NULL DEFAULT 'APP',
    cliente_externo_nombre   VARCHAR(150),
    cliente_externo_telefono VARCHAR(20),
    rango_horario            TSTZRANGE NOT NULL,
    estado                   VARCHAR(20) NOT NULL,
    cupon_id                 UUID,
    precio_base              NUMERIC(10,2) NOT NULL DEFAULT 0.00,
    monto_descuento          NUMERIC(10,2) NOT NULL DEFAULT 0.00,
    monto_total              NUMERIC(10,2) NOT NULL DEFAULT 0.00,
    monto_adelanto           NUMERIC(10,2) NOT NULL DEFAULT 0.00,
    saldo_pendiente          NUMERIC(10,2) NOT NULL DEFAULT 0.00,
    metodo_pago_saldo        VARCHAR(25),
    saldo_pagado_at          TIMESTAMPTZ,
    terminos_aceptados_at    TIMESTAMPTZ,
    hold_expira_at           TIMESTAMPTZ,
    por_validar_desde        TIMESTAMPTZ,
    codigo_qr                VARCHAR(120) UNIQUE,
    checkin_at               TIMESTAMPTZ,
    checkin_por              UUID REFERENCES usuarios(id) ON DELETE RESTRICT,
    motivo_bloqueo           VARCHAR(255),
    cancelado_at             TIMESTAMPTZ,
    motivo_cancelacion       TEXT,
    reembolso_pendiente      BOOLEAN NOT NULL DEFAULT false,
    reembolso_resuelto_at    TIMESTAMPTZ,
    reprogramaciones_count   SMALLINT NOT NULL DEFAULT 0,
    notas_internas           TEXT,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT uq_reservas_id_complejo UNIQUE (id, complejo_id),
    CONSTRAINT fk_reservas_cancha_complejo
        FOREIGN KEY (cancha_id, complejo_id) REFERENCES canchas(id, complejo_id) ON DELETE RESTRICT,
    CONSTRAINT fk_reservas_cupon_complejo
        FOREIGN KEY (cupon_id, complejo_id) REFERENCES cupones(id, complejo_id) ON DELETE RESTRICT,
    CONSTRAINT chk_reservas_origen CHECK (
        origen IN ('APP', 'EXTERNA', 'BLOQUEO_MANTENIMIENTO')
    ),
    CONSTRAINT chk_reservas_estado CHECK (
        estado IN (
            'PENDIENTE_PAGO',
            'POR_VALIDAR',
            'CONFIRMADA',
            'COMPLETADA',
            'EXPIRADA',
            'RECHAZADA',
            'CANCELADA',
            'NO_SHOW'
        )
    ),
    CONSTRAINT chk_reservas_rango_semiabierto CHECK (
        NOT isempty(rango_horario)
        AND NOT lower_inf(rango_horario)
        AND NOT upper_inf(rango_horario)
        AND lower_inc(rango_horario)
        AND NOT upper_inc(rango_horario)
    ),
    CONSTRAINT chk_reservas_origen_coherente CHECK (
        (origen = 'APP' AND jugador_id IS NOT NULL AND terminos_aceptados_at IS NOT NULL)
        OR (origen = 'EXTERNA' AND cliente_externo_nombre IS NOT NULL AND btrim(cliente_externo_nombre) <> '')
        OR (origen = 'BLOQUEO_MANTENIMIENTO' AND motivo_bloqueo IS NOT NULL AND btrim(motivo_bloqueo) <> '')
    ),
    CONSTRAINT chk_reservas_montos_validos CHECK (
        precio_base >= 0
        AND monto_descuento >= 0
        AND monto_total >= 0
        AND monto_adelanto >= 0
        AND saldo_pendiente >= 0
        AND monto_descuento <= precio_base
        AND monto_total = (precio_base - monto_descuento)
        AND monto_adelanto <= monto_total
        AND saldo_pendiente <= monto_total
    ),
    CONSTRAINT chk_reservas_metodo_pago_saldo CHECK (
        metodo_pago_saldo IS NULL
        OR metodo_pago_saldo IN ('EFECTIVO', 'YAPE', 'PLIN', 'TRANSFERENCIA', 'TARJETA', 'OTRO')
    ),
    CONSTRAINT chk_reservas_hold_expira_requerido CHECK (
        estado <> 'PENDIENTE_PAGO' OR hold_expira_at IS NOT NULL
    ),
    CONSTRAINT chk_reservas_reprogramaciones_no_neg CHECK (reprogramaciones_count >= 0),
    CONSTRAINT ex_reservas_cancha_no_solape EXCLUDE USING gist (
        cancha_id WITH =,
        rango_horario WITH &&
    ) WHERE (estado IN ('PENDIENTE_PAGO', 'POR_VALIDAR', 'CONFIRMADA'))
);

CREATE INDEX idx_reservas_complejo_agenda ON reservas (complejo_id, estado, lower(rango_horario));
CREATE INDEX idx_reservas_jugador ON reservas (jugador_id, created_at DESC)
    WHERE jugador_id IS NOT NULL;
CREATE INDEX idx_reservas_hold_expira ON reservas (hold_expira_at)
    WHERE estado = 'PENDIENTE_PAGO';
CREATE INDEX idx_reservas_por_validar ON reservas (complejo_id, por_validar_desde)
    WHERE estado = 'POR_VALIDAR';

CREATE TRIGGER trg_reservas_updated_at
    BEFORE UPDATE ON reservas
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

-- ============================================================================
-- TABLA 12: comprobantes_pago
-- Evidencias manuales (Yape/Plin/banco) y confirmaciones automáticas de pasarela
-- ============================================================================
CREATE TABLE comprobantes_pago (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    reserva_id            UUID NOT NULL,
    complejo_id           UUID NOT NULL REFERENCES complejos(id) ON DELETE RESTRICT,
    metodo_pago_id        UUID NOT NULL,
    es_automatico         BOOLEAN NOT NULL DEFAULT false,
    estado_revision       VARCHAR(20) NOT NULL DEFAULT 'PENDIENTE',
    monto_declarado       NUMERIC(10,2) NOT NULL,
    codigo_operacion      VARCHAR(80),
    pasarela_pago_id      VARCHAR(120),
    evidencia_url         TEXT,
    evidencia_storage_key TEXT,
    validado_por          UUID REFERENCES usuarios(id) ON DELETE RESTRICT,
    validado_at           TIMESTAMPTZ,
    motivo_rechazo        VARCHAR(255),
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT fk_comprobantes_reserva_complejo
        FOREIGN KEY (reserva_id, complejo_id) REFERENCES reservas(id, complejo_id) ON DELETE CASCADE,
    CONSTRAINT fk_comprobantes_metodo_complejo
        FOREIGN KEY (metodo_pago_id, complejo_id) REFERENCES metodos_pago_complejo(id, complejo_id) ON DELETE RESTRICT,
    CONSTRAINT chk_comprobantes_estado_revision CHECK (
        estado_revision IN ('PENDIENTE', 'APROBADO', 'RECHAZADO')
    ),
    CONSTRAINT chk_comprobantes_monto_positivo CHECK (monto_declarado > 0),
    CONSTRAINT chk_comprobantes_motivo_rechazo CHECK (
        estado_revision <> 'RECHAZADO'
        OR (motivo_rechazo IS NOT NULL AND btrim(motivo_rechazo) <> '')
    )
);

CREATE UNIQUE INDEX uq_comprobantes_pasarela_pago
    ON comprobantes_pago (metodo_pago_id, pasarela_pago_id)
    WHERE pasarela_pago_id IS NOT NULL;
CREATE INDEX idx_comprobantes_complejo_estado
    ON comprobantes_pago (complejo_id, estado_revision, created_at DESC);
CREATE INDEX idx_comprobantes_reserva ON comprobantes_pago (reserva_id);

-- ============================================================================
-- TABLA 13: reserva_auditoria
-- Bitácora inmutable de cambios de estado y reprogramaciones (cancha y rango)
-- ============================================================================
CREATE TABLE reserva_auditoria (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    reserva_id         UUID NOT NULL REFERENCES reservas(id) ON DELETE CASCADE,
    complejo_id        UUID NOT NULL REFERENCES complejos(id) ON DELETE CASCADE,
    accion             VARCHAR(40) NOT NULL,
    estado_anterior    VARCHAR(20),
    estado_nuevo       VARCHAR(20) NOT NULL,
    cancha_anterior_id UUID REFERENCES canchas(id) ON DELETE SET NULL,
    cancha_nueva_id    UUID REFERENCES canchas(id) ON DELETE SET NULL,
    rango_anterior     TSTZRANGE,
    rango_nuevo        TSTZRANGE,
    actor_id           UUID REFERENCES usuarios(id) ON DELETE SET NULL,
    motivo             TEXT,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_reserva_auditoria_reserva ON reserva_auditoria (reserva_id, created_at DESC);
CREATE INDEX idx_reserva_auditoria_complejo ON reserva_auditoria (complejo_id, created_at DESC);

-- ============================================================================
-- TABLA 14: notificaciones
-- Bandeja in-app MVP por usuario y complejo
-- ============================================================================
CREATE TABLE notificaciones (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    usuario_id  UUID NOT NULL REFERENCES usuarios(id) ON DELETE CASCADE,
    complejo_id UUID REFERENCES complejos(id) ON DELETE CASCADE,
    reserva_id  UUID REFERENCES reservas(id) ON DELETE SET NULL,
    tipo        VARCHAR(40) NOT NULL,
    titulo      VARCHAR(140) NOT NULL,
    mensaje     TEXT NOT NULL,
    leida_at    TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_notificaciones_no_leidas ON notificaciones (usuario_id, created_at DESC)
    WHERE leida_at IS NULL;
CREATE INDEX idx_notificaciones_usuario ON notificaciones (usuario_id, created_at DESC);

-- ============================================================================
-- TABLA 15: push_subscriptions
-- Suscripciones Web Push PWA (W3C VAPID, relación 1:N con usuarios)
-- ============================================================================
CREATE TABLE push_subscriptions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    usuario_id      UUID NOT NULL REFERENCES usuarios(id) ON DELETE CASCADE,
    endpoint        TEXT NOT NULL UNIQUE,
    p256dh          TEXT NOT NULL,
    auth            TEXT NOT NULL,
    user_agent      VARCHAR(255),
    activa          BOOLEAN NOT NULL DEFAULT true,
    ultimo_envio_at TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_push_subscriptions_usuario_activas ON push_subscriptions (usuario_id)
    WHERE activa = true;

CREATE TRIGGER trg_push_subscriptions_updated_at
    BEFORE UPDATE ON push_subscriptions
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

COMMIT;
