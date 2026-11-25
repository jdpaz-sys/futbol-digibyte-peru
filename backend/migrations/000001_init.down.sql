-- ============================================================================
-- Fútbol Digibyte Perú — Migración Inicial 000001_init (DOWN)
-- Revierte limpiamente las 15 tablas de negocio y función auxiliar en orden inverso
-- ============================================================================

BEGIN;

DROP TABLE IF EXISTS push_subscriptions;
DROP TABLE IF EXISTS notificaciones;
DROP TABLE IF EXISTS reserva_auditoria;
DROP TABLE IF EXISTS comprobantes_pago;
DROP TABLE IF EXISTS reservas;
DROP TABLE IF EXISTS cupones;
DROP TABLE IF EXISTS tarifas_franja;
DROP TABLE IF EXISTS horarios_atencion;
DROP TABLE IF EXISTS canchas;
DROP TABLE IF EXISTS metodos_pago_complejo;
DROP TABLE IF EXISTS suscripciones_historial;
DROP TABLE IF EXISTS complejo_fotos;
DROP TABLE IF EXISTS complejos;
DROP TABLE IF EXISTS planes_suscripcion;
DROP TABLE IF EXISTS usuarios;

DROP FUNCTION IF EXISTS fn_set_updated_at();

COMMIT;
