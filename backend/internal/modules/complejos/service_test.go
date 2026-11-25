package complejos

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/jdpaz-sys/futbol-digibyte-peru/backend/internal/db"
	apphttp "github.com/jdpaz-sys/futbol-digibyte-peru/backend/internal/http"
)

const testAESKeyHex = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// ---------- Repositorio en memoria para pruebas unitarias y de aislamiento multi-tenant ----------

type memoryRepo struct {
	complejos   map[uuid.UUID]ComplejoPerfil
	canchas     map[uuid.UUID]Cancha
	horarios    map[uuid.UUID][]HorarioAtencion
	tarifas     map[uuid.UUID]TarifaFranja
	cupones     map[uuid.UUID]Cupon
	metodos     map[uuid.UUID]MetodoPago
	rawCreds    map[uuid.UUID][]byte
	owners      map[uuid.UUID]uuid.UUID // complejoID -> duenoID
}

func newMemoryRepo() *memoryRepo {
	return &memoryRepo{
		complejos: make(map[uuid.UUID]ComplejoPerfil),
		canchas:   make(map[uuid.UUID]Cancha),
		horarios:  make(map[uuid.UUID][]HorarioAtencion),
		tarifas:   make(map[uuid.UUID]TarifaFranja),
		cupones:   make(map[uuid.UUID]Cupon),
		metodos:   make(map[uuid.UUID]MetodoPago),
		rawCreds:  make(map[uuid.UUID][]byte),
		owners:    make(map[uuid.UUID]uuid.UUID),
	}
}

func (m *memoryRepo) seedComplejo(duenoID, complejoID uuid.UUID, nombre string) ComplejoPerfil {
	now := time.Now().UTC()
	p := ComplejoPerfil{
		ID:                  complejoID,
		DuenoID:             duenoID,
		Slug:                strings.ToLower(strings.ReplaceAll(nombre, " ", "-")),
		Nombre:              nombre,
		Servicios:           []string{},
		Departamento:        "Lima",
		Provincia:           "Huaura",
		Distrito:            "Huacho",
		Publicado:           false,
		UnidadReservaMin:    60,
		HoldDuracionMin:     10,
		TipoAdelanto:        TipoAdelantoPorcentaje,
		ValorAdelanto:       50,
		HorasMinCancelacion: 24,
		EstadoSuscripcion:   "PRUEBA",
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	m.complejos[complejoID] = p
	m.owners[complejoID] = duenoID
	return m.withChecklist(p)
}

func (m *memoryRepo) countsFor(complejoID uuid.UUID) ChecklistCounts {
	var counts ChecklistCounts
	for _, c := range m.canchas {
		if c.ComplejoID == complejoID && c.Activa {
			counts.CanchasActivas++
		}
	}
	for _, h := range m.horarios[complejoID] {
		if !h.Cerrado {
			counts.HorariosAbiertos++
		}
	}
	for _, t := range m.tarifas {
		if t.ComplejoID == complejoID && t.Activa {
			counts.TarifasActivas++
		}
	}
	for _, mp := range m.metodos {
		if mp.ComplejoID == complejoID && mp.Activo {
			counts.MetodosPagoActivos++
		}
	}
	return counts
}

func (m *memoryRepo) withChecklist(p ComplejoPerfil) ComplejoPerfil {
	p.ChecklistPublicacion = EvaluatePublishChecklist(p, m.countsFor(p.ID))
	return p
}

func (m *memoryRepo) GetComplejo(_ context.Context, complejoID uuid.UUID) (ComplejoPerfil, error) {
	p, ok := m.complejos[complejoID]
	if !ok {
		return ComplejoPerfil{}, ErrComplejoNotFound
	}
	return m.withChecklist(p), nil
}

func (m *memoryRepo) UpdateComplejo(_ context.Context, params UpdateComplejoParams) (ComplejoPerfil, error) {
	p, ok := m.complejos[params.ComplejoID]
	if !ok {
		return ComplejoPerfil{}, ErrComplejoNotFound
	}
	p.Nombre = params.Nombre
	p.Descripcion = params.Descripcion
	p.TelefonoContacto = params.TelefonoContacto
	p.Servicios = normalizeServiciosSlice(params.Servicios)
	p.Departamento = params.Departamento
	p.Provincia = params.Provincia
	p.Distrito = params.Distrito
	if params.Lat != nil && params.Lng != nil {
		lat, lng := *params.Lat, *params.Lng
		p.Lat = &lat
		p.Lng = &lng
	}
	p.DireccionEscrita = params.DireccionEscrita
	p.Referencias = params.Referencias
	p.UnidadReservaMin = params.UnidadReservaMin
	p.HoldDuracionMin = params.HoldDuracionMin
	p.TipoAdelanto = params.TipoAdelanto
	p.ValorAdelanto = params.ValorAdelanto
	p.HorasMinCancelacion = params.HorasMinCancelacion
	p.TerminosReserva = params.TerminosReserva
	p.UpdatedAt = time.Now().UTC()
	m.complejos[params.ComplejoID] = p
	return m.withChecklist(p), nil
}

func (m *memoryRepo) PublishComplejo(_ context.Context, complejoID uuid.UUID) (ComplejoPerfil, error) {
	p, ok := m.complejos[complejoID]
	if !ok {
		return ComplejoPerfil{}, ErrComplejoNotFound
	}
	p = m.withChecklist(p)
	if !p.ChecklistPublicacion.ListoParaPublicar {
		return ComplejoPerfil{}, &PublishRequirementsError{
			Message:             "El complejo no cumple los requisitos mínimos para publicarse (RN-03)",
			RequisitosFaltantes: p.ChecklistPublicacion.RequisitosFaltantes,
		}
	}
	p.Publicado = true
	p.UpdatedAt = time.Now().UTC()
	m.complejos[complejoID] = p
	return m.withChecklist(p), nil
}

func (m *memoryRepo) UnpublishComplejo(_ context.Context, complejoID uuid.UUID) (ComplejoPerfil, error) {
	p, ok := m.complejos[complejoID]
	if !ok {
		return ComplejoPerfil{}, ErrComplejoNotFound
	}
	p.Publicado = false
	p.UpdatedAt = time.Now().UTC()
	m.complejos[complejoID] = p
	return m.withChecklist(p), nil
}

func (m *memoryRepo) ListCanchas(_ context.Context, complejoID uuid.UUID, activa *bool) ([]Cancha, error) {
	out := make([]Cancha, 0)
	for _, c := range m.canchas {
		if c.ComplejoID != complejoID {
			continue
		}
		if activa != nil && c.Activa != *activa {
			continue
		}
		out = append(out, c)
	}
	return out, nil
}

func (m *memoryRepo) GetCancha(_ context.Context, complejoID, canchaID uuid.UUID) (Cancha, error) {
	c, ok := m.canchas[canchaID]
	if !ok || c.ComplejoID != complejoID {
		return Cancha{}, ErrCanchaNotFound
	}
	return c, nil
}

func (m *memoryRepo) CreateCancha(_ context.Context, c Cancha) (Cancha, error) {
	now := time.Now().UTC()
	c.ID = uuid.New()
	c.CreatedAt = now
	c.UpdatedAt = now
	m.canchas[c.ID] = c
	return c, nil
}

func (m *memoryRepo) UpdateCancha(_ context.Context, c Cancha) (Cancha, error) {
	existing, ok := m.canchas[c.ID]
	if !ok || existing.ComplejoID != c.ComplejoID {
		return Cancha{}, ErrCanchaNotFound
	}
	c.UpdatedAt = time.Now().UTC()
	m.canchas[c.ID] = c
	return c, nil
}

func (m *memoryRepo) DeactivateCancha(_ context.Context, complejoID, canchaID uuid.UUID) (Cancha, error) {
	c, ok := m.canchas[canchaID]
	if !ok || c.ComplejoID != complejoID {
		return Cancha{}, ErrCanchaNotFound
	}
	c.Activa = false
	c.UpdatedAt = time.Now().UTC()
	m.canchas[canchaID] = c
	return c, nil
}

func (m *memoryRepo) ListHorarios(_ context.Context, complejoID uuid.UUID) ([]HorarioAtencion, error) {
	list := m.horarios[complejoID]
	if list == nil {
		return make([]HorarioAtencion, 0), nil
	}
	return append([]HorarioAtencion(nil), list...), nil
}

func (m *memoryRepo) UpsertHorarios(_ context.Context, complejoID uuid.UUID, horarios []HorarioAtencion) ([]HorarioAtencion, error) {
	now := time.Now().UTC()
	out := make([]HorarioAtencion, 0, len(horarios))
	for _, h := range horarios {
		h.ID = uuid.New()
		h.ComplejoID = complejoID
		h.CreatedAt = now
		h.UpdatedAt = now
		out = append(out, h)
	}
	m.horarios[complejoID] = out
	return append([]HorarioAtencion(nil), out...), nil
}

func (m *memoryRepo) ListTarifas(_ context.Context, complejoID uuid.UUID, filter TarifasFilter) ([]TarifaFranja, error) {
	out := make([]TarifaFranja, 0)
	for _, t := range m.tarifas {
		if t.ComplejoID != complejoID {
			continue
		}
		if filter.CanchaID != nil && (t.CanchaID == nil || *t.CanchaID != *filter.CanchaID) {
			continue
		}
		if filter.DiaSemana != nil && t.DiaSemana != *filter.DiaSemana {
			continue
		}
		if filter.Activa != nil && t.Activa != *filter.Activa {
			continue
		}
		out = append(out, t)
	}
	return out, nil
}

func (m *memoryRepo) GetTarifa(_ context.Context, complejoID, tarifaID uuid.UUID) (TarifaFranja, error) {
	t, ok := m.tarifas[tarifaID]
	if !ok || t.ComplejoID != complejoID {
		return TarifaFranja{}, ErrTarifaNotFound
	}
	return t, nil
}

func sameCanchaScope(a, b *uuid.UUID) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func (m *memoryRepo) hasTarifaOverlap(candidate TarifaFranja, ignoreID uuid.UUID) bool {
	if !candidate.Activa {
		return false
	}
	for _, existing := range m.tarifas {
		if existing.ID == ignoreID || !existing.Activa {
			continue
		}
		if existing.ComplejoID != candidate.ComplejoID || existing.DiaSemana != candidate.DiaSemana {
			continue
		}
		if !sameCanchaScope(existing.CanchaID, candidate.CanchaID) {
			continue
		}
		// Rangos semiabiertos [inicio, fin) se solapan si inicioA < finB && inicioB < finA
		if candidate.MinutoInicio < existing.MinutoFin && existing.MinutoInicio < candidate.MinutoFin {
			return true
		}
	}
	return false
}

func (m *memoryRepo) CreateTarifa(_ context.Context, t TarifaFranja) (TarifaFranja, error) {
	if m.hasTarifaOverlap(t, uuid.Nil) {
		return TarifaFranja{}, ErrTarifaOverlap
	}
	now := time.Now().UTC()
	t.ID = uuid.New()
	t.CreatedAt = now
	t.UpdatedAt = now
	m.tarifas[t.ID] = t
	return t, nil
}

func (m *memoryRepo) UpdateTarifa(_ context.Context, t TarifaFranja) (TarifaFranja, error) {
	existing, ok := m.tarifas[t.ID]
	if !ok || existing.ComplejoID != t.ComplejoID {
		return TarifaFranja{}, ErrTarifaNotFound
	}
	if m.hasTarifaOverlap(t, t.ID) {
		return TarifaFranja{}, ErrTarifaOverlap
	}
	t.UpdatedAt = time.Now().UTC()
	m.tarifas[t.ID] = t
	return t, nil
}

func (m *memoryRepo) DeleteTarifa(_ context.Context, complejoID, tarifaID uuid.UUID) error {
	existing, ok := m.tarifas[tarifaID]
	if !ok || existing.ComplejoID != complejoID {
		return ErrTarifaNotFound
	}
	delete(m.tarifas, tarifaID)
	return nil
}

func (m *memoryRepo) ListCupones(_ context.Context, complejoID uuid.UUID, activo *bool) ([]Cupon, error) {
	out := make([]Cupon, 0)
	for _, c := range m.cupones {
		if c.ComplejoID != complejoID {
			continue
		}
		if activo != nil && c.Activo != *activo {
			continue
		}
		out = append(out, c)
	}
	return out, nil
}

func (m *memoryRepo) GetCupon(_ context.Context, complejoID, cuponID uuid.UUID) (Cupon, error) {
	c, ok := m.cupones[cuponID]
	if !ok || c.ComplejoID != complejoID {
		return Cupon{}, ErrCuponNotFound
	}
	return c, nil
}

func (m *memoryRepo) CreateCupon(_ context.Context, c Cupon) (Cupon, error) {
	for _, existing := range m.cupones {
		if existing.ComplejoID == c.ComplejoID && existing.Codigo == c.Codigo {
			return Cupon{}, ErrCuponCodeAlreadyExists
		}
	}
	now := time.Now().UTC()
	c.ID = uuid.New()
	c.CreatedAt = now
	c.UpdatedAt = now
	m.cupones[c.ID] = c
	return c, nil
}

func (m *memoryRepo) UpdateCupon(_ context.Context, c Cupon) (Cupon, error) {
	existing, ok := m.cupones[c.ID]
	if !ok || existing.ComplejoID != c.ComplejoID {
		return Cupon{}, ErrCuponNotFound
	}
	for _, other := range m.cupones {
		if other.ID != c.ID && other.ComplejoID == c.ComplejoID && other.Codigo == c.Codigo {
			return Cupon{}, ErrCuponCodeAlreadyExists
		}
	}
	c.UpdatedAt = time.Now().UTC()
	m.cupones[c.ID] = c
	return c, nil
}

func (m *memoryRepo) ListMetodosPago(_ context.Context, complejoID uuid.UUID, activo *bool) ([]MetodoPago, error) {
	out := make([]MetodoPago, 0)
	for _, mp := range m.metodos {
		if mp.ComplejoID != complejoID {
			continue
		}
		if activo != nil && mp.Activo != *activo {
			continue
		}
		out = append(out, mp)
	}
	return out, nil
}

func (m *memoryRepo) GetMetodoPago(_ context.Context, complejoID, metodoID uuid.UUID) (MetodoPago, error) {
	mp, ok := m.metodos[metodoID]
	if !ok || mp.ComplejoID != complejoID {
		return MetodoPago{}, ErrMetodoPagoNotFound
	}
	return mp, nil
}

func (m *memoryRepo) CreateMetodoPago(_ context.Context, p SaveMetodoPagoParams) (MetodoPago, error) {
	now := time.Now().UTC()
	id := uuid.New()
	if len(p.CredencialesCifradas) > 0 {
		m.rawCreds[id] = append([]byte(nil), p.CredencialesCifradas...)
	}
	mp := MetodoPago{
		ID:                id,
		ComplejoID:        p.ComplejoID,
		Tipo:              p.Tipo,
		Etiqueta:          p.Etiqueta,
		Titular:           p.Titular,
		EntidadFinanciera: p.EntidadFinanciera,
		NumeroDestino:     p.NumeroDestino,
		CCI:               p.CCI,
		QRURL:             p.QRURL,
		QRStorageKey:      p.QRStorageKey,
		LlavePublica:      p.LlavePublica,
		TieneCredenciales: len(m.rawCreds[id]) > 0,
		Instrucciones:     p.Instrucciones,
		Activo:            p.Activo,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	m.metodos[id] = mp
	return mp, nil
}

func (m *memoryRepo) UpdateMetodoPago(_ context.Context, p SaveMetodoPagoParams) (MetodoPago, error) {
	existing, ok := m.metodos[p.ID]
	if !ok || existing.ComplejoID != p.ComplejoID {
		return MetodoPago{}, ErrMetodoPagoNotFound
	}
	if p.UpdateCredenciales {
		m.rawCreds[p.ID] = append([]byte(nil), p.CredencialesCifradas...)
	}
	existing.Etiqueta = p.Etiqueta
	existing.Titular = p.Titular
	existing.EntidadFinanciera = p.EntidadFinanciera
	existing.NumeroDestino = p.NumeroDestino
	existing.CCI = p.CCI
	existing.QRURL = p.QRURL
	existing.QRStorageKey = p.QRStorageKey
	existing.LlavePublica = p.LlavePublica
	existing.TieneCredenciales = len(m.rawCreds[p.ID]) > 0
	existing.Instrucciones = p.Instrucciones
	existing.Activo = p.Activo
	existing.UpdatedAt = time.Now().UTC()
	m.metodos[p.ID] = existing
	return existing, nil
}

func (m *memoryRepo) DeactivateMetodoPago(_ context.Context, complejoID, metodoID uuid.UUID) (MetodoPago, error) {
	mp, ok := m.metodos[metodoID]
	if !ok || mp.ComplejoID != complejoID {
		return MetodoPago{}, ErrMetodoPagoNotFound
	}
	mp.Activo = false
	mp.UpdatedAt = time.Now().UTC()
	m.metodos[metodoID] = mp
	return mp, nil
}

// ---------- Helpers de test ----------

func mustNewTestCipher(t *testing.T) *db.CredentialsCipher {
	t.Helper()
	key, err := hex.DecodeString(testAESKeyHex)
	if err != nil {
		t.Fatalf("hex.DecodeString: %v", err)
	}
	c, err := db.NewCredentialsCipher(key)
	if err != nil {
		t.Fatalf("NewCredentialsCipher: %v", err)
	}
	return c
}

func ptrStr(s string) *string    { return &s }
func ptrInt(v int) *int          { return &v }
func ptrF64(v float64) *float64  { return &v }
func ptrBool(v bool) *bool       { return &v }

// ---------- Pruebas unitarias ----------

func TestValidateCoordinatesAndAdelantos(t *testing.T) {
	// Coordenadas válidas (Huacho)
	if errs := ValidateCoordinates(ptrF64(-11.1067), ptrF64(-77.6088)); len(errs) != 0 {
		t.Fatalf("coordenadas válidas de Huacho devolvieron error: %v", errs)
	}
	// Solo latitud sin longitud
	if errs := ValidateCoordinates(ptrF64(-11.1067), nil); errs["lng"] == "" {
		t.Fatalf("debió rechazar lat sin lng, obtenido: %v", errs)
	}
	// Fuera de rango WGS84
	if errs := ValidateCoordinates(ptrF64(-91.0), ptrF64(181.0)); errs["lat"] == "" || errs["lng"] == "" {
		t.Fatalf("debió rechazar lat/lng fuera de rango, obtenido: %v", errs)
	}
	// NaN / Inf
	if errs := ValidateCoordinates(ptrF64(math.NaN()), ptrF64(math.Inf(1))); errs["lat"] == "" || errs["lng"] == "" {
		t.Fatalf("debió rechazar NaN/Inf, obtenido: %v", errs)
	}

	// Reglas de adelanto (RF-06)
	if !IsValidAdelanto(TipoAdelantoPorcentaje, 1) || !IsValidAdelanto(TipoAdelantoPorcentaje, 50) || !IsValidAdelanto(TipoAdelantoPorcentaje, 100) {
		t.Fatal("porcentajes 1, 50 y 100 deben ser válidos")
	}
	if IsValidAdelanto(TipoAdelantoPorcentaje, 0) || IsValidAdelanto(TipoAdelantoPorcentaje, 100.5) || IsValidAdelanto(TipoAdelantoPorcentaje, -5) {
		t.Fatal("porcentajes fuera de 1..100 deben ser rechazados")
	}
	if !IsValidAdelanto(TipoAdelantoMontoFijo, 25.5) {
		t.Fatal("monto fijo positivo debe ser válido")
	}
	if IsValidAdelanto(TipoAdelantoMontoFijo, 0) || IsValidAdelanto(TipoAdelantoMontoFijo, -10) || IsValidAdelanto("OTRO", 50) {
		t.Fatal("monto fijo <= 0 o tipo inválido deben ser rechazados")
	}
}

func TestUpdateComplejoValidations(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepo()
	duenoID, complejoID := uuid.New(), uuid.New()
	repo.seedComplejo(duenoID, complejoID, "Complejo Huacho")
	svc := NewService(repo, mustNewTestCipher(t))

	// Referencias > 150 caracteres (UTF-8 con tildes)
	longRef := strings.Repeat("á", 151)
	_, err := svc.UpdateComplejo(ctx, complejoID, UpdateComplejoRequest{
		Referencias: &longRef,
	})
	var valErr *ValidationError
	if !errors.As(err, &valErr) || valErr.Fields["referencias"] == "" {
		t.Fatalf("debió rechazar referencias > 150 chars, obtenido: %v", err)
	}

	// Cambiar a MONTO_FIJO 150 y luego intentar volver a PORCENTAJE sin ajustar valor_adelanto -> debe fallar
	_, err = svc.UpdateComplejo(ctx, complejoID, UpdateComplejoRequest{
		TipoAdelanto:  ptrStr("MONTO_FIJO"),
		ValorAdelanto: ptrF64(150),
	})
	if err != nil {
		t.Fatalf("actualización válida a MONTO_FIJO falló: %v", err)
	}

	_, err = svc.UpdateComplejo(ctx, complejoID, UpdateComplejoRequest{
		TipoAdelanto: ptrStr("PORCENTAJE"),
	})
	if !errors.As(err, &valErr) || valErr.Fields["valor_adelanto"] == "" {
		t.Fatalf("debió detectar valor_adelanto=150 incompatible con PORCENTAJE, obtenido: %v", err)
	}

	// Unidad de reserva inválida (45) y hold inválido (4)
	_, err = svc.UpdateComplejo(ctx, complejoID, UpdateComplejoRequest{
		UnidadReservaMin: ptrInt(45),
		HoldDuracionMin:  ptrInt(4),
	})
	if !errors.As(err, &valErr) || valErr.Fields["unidad_reserva_min"] == "" || valErr.Fields["hold_duracion_min"] == "" {
		t.Fatalf("debió rechazar unidad_reserva_min=45 y hold_duracion_min=4, obtenido: %v", err)
	}
}

func TestPublishChecklistRN03(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepo()
	duenoID, complejoID := uuid.New(), uuid.New()
	repo.seedComplejo(duenoID, complejoID, "Sport Arena Huacho")
	svc := NewService(repo, mustNewTestCipher(t))

	// 1. Complejo recién creado: faltan 7 requisitos (el adelanto default 50% sí es válido)
	_, err := svc.PublishComplejo(ctx, complejoID)
	var pubErr *PublishRequirementsError
	if !errors.As(err, &pubErr) {
		t.Fatalf("PublishComplejo incompleto debía devolver PublishRequirementsError, obtenido: %v", err)
	}
	if len(pubErr.RequisitosFaltantes) != 7 {
		t.Fatalf("faltantes = %d (%v), esperado 7", len(pubErr.RequisitosFaltantes), pubErr.RequisitosFaltantes)
	}

	// 2. Verificar también que adelanto inválido se reporta explícitamente
	badProfile := ComplejoPerfil{
		Lat:              ptrF64(-11.1067),
		Lng:              ptrF64(-77.6088),
		DireccionEscrita: ptrStr("Av. 28 de Julio 450"),
		TerminosReserva:  ptrStr("Términos válidos"),
		TipoAdelanto:     TipoAdelantoPorcentaje,
		ValorAdelanto:    0,
	}
	cl := EvaluatePublishChecklist(badProfile, ChecklistCounts{
		CanchasActivas: 1, HorariosAbiertos: 1, TarifasActivas: 1, MetodosPagoActivos: 1,
	})
	if cl.ListoParaPublicar || len(cl.RequisitosFaltantes) != 1 || cl.RequisitosFaltantes[0].Codigo != ReqAdelantoInvalido {
		t.Fatalf("checklist debía marcar únicamente ADELANTO_INVALIDO, obtenido: %+v", cl)
	}

	// 3. Completar todos los requisitos RN-03 en el complejo del repo
	if _, err := svc.UpdateComplejo(ctx, complejoID, UpdateComplejoRequest{
		Lat:              ptrF64(-11.1067),
		Lng:              ptrF64(-77.6088),
		DireccionEscrita: ptrStr("Av. 28 de Julio 450, Huacho"),
		Referencias:      ptrStr("A media cuadra de la Plaza de Armas"),
		TerminosReserva:  ptrStr("Adelanto no reembolsable con menos de 24h."),
	}); err != nil {
		t.Fatalf("UpdateComplejo: %v", err)
	}
	cancha, err := svc.CreateCancha(ctx, complejoID, CreateCanchaRequest{
		Nombre:    "Cancha Principal 7v7",
		Modalidad: "7v7",
	})
	if err != nil {
		t.Fatalf("CreateCancha: %v", err)
	}

	dias := make([]HorarioDiaInput, 7)
	for d := 0; d < 7; d++ {
		dias[d] = HorarioDiaInput{DiaSemana: d, MinutoApertura: 480, MinutoCierre: 1380, Cerrado: false}
	}
	if _, err := svc.UpsertHorarios(ctx, complejoID, UpsertHorariosRequest{Horarios: dias}); err != nil {
		t.Fatalf("UpsertHorarios: %v", err)
	}

	canchaStr := cancha.ID.String()
	if _, err := svc.CreateTarifa(ctx, complejoID, CreateTarifaRequest{
		CanchaID:     &canchaStr,
		DiaSemana:    ptrInt(1),
		MinutoInicio: ptrInt(480),
		MinutoFin:    ptrInt(1080),
		PrecioHora:   ptrF64(90),
		Etiqueta:     ptrStr("Tarifa Día"),
	}); err != nil {
		t.Fatalf("CreateTarifa: %v", err)
	}

	if _, err := svc.CreateMetodoPago(ctx, complejoID, CreateMetodoPagoRequest{
		Tipo:          "YAPE",
		Titular:       ptrStr("Sport Arena SAC"),
		NumeroDestino: ptrStr("999888777"),
	}); err != nil {
		t.Fatalf("CreateMetodoPago: %v", err)
	}

	// 4. Ahora publicar debe triunfar
	published, err := svc.PublishComplejo(ctx, complejoID)
	if err != nil {
		t.Fatalf("PublishComplejo completo falló: %v", err)
	}
	if !published.Publicado || !published.ChecklistPublicacion.ListoParaPublicar {
		t.Fatalf("complejo debía quedar publicado=true y listo=true, obtenido: %+v", published)
	}

	// 5. Despublicar debe marcar publicado=false
	unpublished, err := svc.UnpublishComplejo(ctx, complejoID)
	if err != nil || unpublished.Publicado {
		t.Fatalf("UnpublishComplejo debía marcar publicado=false, err=%v, pub=%v", err, unpublished.Publicado)
	}
}

func TestTarifasValidationAndOverlap(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepo()
	duenoID, complejoID := uuid.New(), uuid.New()
	repo.seedComplejo(duenoID, complejoID, "Canchas Norte")
	svc := NewService(repo, mustNewTestCipher(t))

	// Franja inválida: inicio >= fin y precio <= 0
	_, err := svc.CreateTarifa(ctx, complejoID, CreateTarifaRequest{
		DiaSemana:    ptrInt(2),
		MinutoInicio: ptrInt(1080),
		MinutoFin:    ptrInt(1080),
		PrecioHora:   ptrF64(0),
	})
	var valErr *ValidationError
	if !errors.As(err, &valErr) || valErr.Fields["minuto_fin"] == "" || valErr.Fields["precio_hora"] == "" {
		t.Fatalf("debió rechazar franja y precio inválidos, obtenido: %v", err)
	}

	// Tarifa día 08:00-18:00 [480, 1080)
	if _, err := svc.CreateTarifa(ctx, complejoID, CreateTarifaRequest{
		DiaSemana:    ptrInt(5),
		MinutoInicio: ptrInt(480),
		MinutoFin:    ptrInt(1080),
		PrecioHora:   ptrF64(80),
	}); err != nil {
		t.Fatalf("tarifa día falló: %v", err)
	}

	// Tarifa noche contigua 18:00-23:00 [1080, 1380) -> NO se solapa
	if _, err := svc.CreateTarifa(ctx, complejoID, CreateTarifaRequest{
		DiaSemana:    ptrInt(5),
		MinutoInicio: ptrInt(1080),
		MinutoFin:    ptrInt(1380),
		PrecioHora:   ptrF64(120),
	}); err != nil {
		t.Fatalf("tarifa noche contigua falló: %v", err)
	}

	// Tarifa solapada 17:00-20:00 [1020, 1200) -> ErrTarifaOverlap
	_, err = svc.CreateTarifa(ctx, complejoID, CreateTarifaRequest{
		DiaSemana:    ptrInt(5),
		MinutoInicio: ptrInt(1020),
		MinutoFin:    ptrInt(1200),
		PrecioHora:   ptrF64(100),
	})
	if !errors.Is(err, ErrTarifaOverlap) {
		t.Fatalf("tarifa solapada debía devolver ErrTarifaOverlap, obtenido: %v", err)
	}
}

func TestMetodosPagoAES256GCMEncryptionAndNoLeak(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepo()
	duenoID, complejoID := uuid.New(), uuid.New()
	repo.seedComplejo(duenoID, complejoID, "Arena Culqi")

	cipherObj := mustNewTestCipher(t)
	svc := NewService(repo, cipherObj)

	secretValue := "sk_test_super_secreto_culqi_98765"
	metodo, err := svc.CreateMetodoPago(ctx, complejoID, CreateMetodoPagoRequest{
		Tipo:         "culqi",
		Etiqueta:     ptrStr("Culqi Tarjetas"),
		LlavePublica: ptrStr("pk_test_123456"),
		Credenciales: json.RawMessage(`{"secret_key":"` + secretValue + `"}`),
	})
	if err != nil {
		t.Fatalf("CreateMetodoPago CULQI falló: %v", err)
	}

	if !metodo.TieneCredenciales {
		t.Fatal("esperado tiene_credenciales=true")
	}

	// Verificar que el JSON serializado jamás expone credenciales ni campos secretos
	encoded, err := json.Marshal(metodo)
	if err != nil {
		t.Fatalf("json.Marshal falló: %v", err)
	}
	if strings.Contains(string(encoded), secretValue) || strings.Contains(string(encoded), "credenciales_cifradas") {
		t.Fatalf("la respuesta JSON filtró credenciales sensibles: %s", string(encoded))
	}

	// Verificar que los bytes guardados están realmente cifrados con AES-256-GCM y son descifrables
	storedCiphertext := repo.rawCreds[metodo.ID]
	if len(storedCiphertext) == 0 {
		t.Fatal("no se almacenaron bytes cifrados")
	}
	if bytes.Contains(storedCiphertext, []byte(secretValue)) {
		t.Fatal("los bytes almacenados contienen el secreto en texto plano")
	}
	decrypted, err := cipherObj.Decrypt(storedCiphertext)
	if err != nil {
		t.Fatalf("Decrypt de bytes almacenados falló: %v", err)
	}
	if !strings.Contains(string(decrypted), secretValue) {
		t.Fatalf("credenciales descifradas = %s, no contienen %s", string(decrypted), secretValue)
	}

	// PATCH sin enviar credenciales debe conservar las credenciales cifradas existentes
	updated, err := svc.UpdateMetodoPago(ctx, complejoID, metodo.ID, UpdateMetodoPagoRequest{
		Etiqueta: ptrStr("Culqi Actualizado"),
	})
	if err != nil {
		t.Fatalf("UpdateMetodoPago sin credenciales falló: %v", err)
	}
	if !updated.TieneCredenciales || !bytes.Equal(repo.rawCreds[metodo.ID], storedCiphertext) {
		t.Fatal("PATCH sin credenciales alteró o borró credenciales_cifradas existentes")
	}
}

func TestMultiTenantIsolationBetweenTwoOwners(t *testing.T) {
	repo := newMemoryRepo()
	ownerA, complejoA := uuid.New(), uuid.New()
	ownerB, complejoB := uuid.New(), uuid.New()
	repo.seedComplejo(ownerA, complejoA, "Complejo Alfa")
	repo.seedComplejo(ownerB, complejoB, "Complejo Beta")

	cipherObj := mustNewTestCipher(t)
	svc := NewService(repo, cipherObj)
	handler := NewHandler(svc, slog.New(slog.NewTextHandler(io.Discard, nil)))

	tokenParser := func(token string) (apphttp.AuthUser, error) {
		switch token {
		case "token-owner-a":
			cid := complejoA
			return apphttp.AuthUser{UserID: ownerA, Rol: apphttp.RoleDueno, ComplejoID: &cid}, nil
		case "token-owner-b":
			cid := complejoB
			return apphttp.AuthUser{UserID: ownerB, Rol: apphttp.RoleDueno, ComplejoID: &cid}, nil
		case "token-jugador":
			return apphttp.AuthUser{UserID: uuid.New(), Rol: apphttp.RoleJugador}, nil
		default:
			return apphttp.AuthUser{}, errors.New("token inválido")
		}
	}

	tenantChecker := func(_ context.Context, userID, complejoID uuid.UUID) (bool, error) {
		return repo.owners[complejoID] == userID, nil
	}

	router := apphttp.NewRouter(apphttp.RouterConfig{
		CORSAllowedOrigins: []string{"http://localhost:3000"},
		Logger:             slog.New(slog.NewTextHandler(io.Discard, nil)),
		TokenParser:        tokenParser,
		TenantChecker:      tenantChecker,
		MountDueno: func(dueno chi.Router) {
			handler.RegisterDuenoRoutes(dueno)
		},
	})

	doReq := func(method, path, token, body string, extraHeaders map[string]string) *httptest.ResponseRecorder {
		var rBody io.Reader
		if body != "" {
			rBody = strings.NewReader(body)
		}
		req := httptest.NewRequest(method, path, rBody)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		for k, v := range extraHeaders {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	// 1. Jugador no puede entrar a /api/v1/dueno/* (403)
	if rec := doReq(http.MethodGet, "/api/v1/dueno/complejo", "token-jugador", "", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("JUGADOR debía recibir 403, obtenido: %d", rec.Code)
	}

	// 2. Owner A crea cancha, tarifa, cupón y método de pago
	recCancha := doReq(http.MethodPost, "/api/v1/dueno/canchas", "token-owner-a", `{"nombre":"Cancha A1","modalidad":"7v7"}`, nil)
	if recCancha.Code != http.StatusCreated {
		t.Fatalf("Owner A crear cancha = %d (%s)", recCancha.Code, recCancha.Body.String())
	}
	var canchaResp struct {
		Data Cancha `json:"data"`
	}
	if err := json.Unmarshal(recCancha.Body.Bytes(), &canchaResp); err != nil {
		t.Fatalf("unmarshal cancha: %v", err)
	}
	canchaAID := canchaResp.Data.ID

	recTarifa := doReq(http.MethodPost, "/api/v1/dueno/tarifas", "token-owner-a",
		`{"cancha_id":"`+canchaAID.String()+`","dia_semana":1,"minuto_inicio":480,"minuto_fin":1080,"precio_hora":100}`, nil)
	if recTarifa.Code != http.StatusCreated {
		t.Fatalf("Owner A crear tarifa = %d (%s)", recTarifa.Code, recTarifa.Body.String())
	}
	var tarifaResp struct {
		Data TarifaFranja `json:"data"`
	}
	_ = json.Unmarshal(recTarifa.Body.Bytes(), &tarifaResp)

	recCupon := doReq(http.MethodPost, "/api/v1/dueno/cupones", "token-owner-a",
		`{"codigo":"promo10","tipo_descuento":"PORCENTAJE","valor_descuento":10}`, nil)
	if recCupon.Code != http.StatusCreated {
		t.Fatalf("Owner A crear cupón = %d (%s)", recCupon.Code, recCupon.Body.String())
	}
	var cuponResp struct {
		Data Cupon `json:"data"`
	}
	_ = json.Unmarshal(recCupon.Body.Bytes(), &cuponResp)
	if cuponResp.Data.Codigo != "PROMO10" {
		t.Fatalf("código de cupón no normalizado a mayúsculas: %q", cuponResp.Data.Codigo)
	}

	recMetodo := doReq(http.MethodPost, "/api/v1/dueno/metodos-pago", "token-owner-a",
		`{"tipo":"YAPE","titular":"Owner A","numero_destino":"999111222"}`, nil)
	if recMetodo.Code != http.StatusCreated {
		t.Fatalf("Owner A crear método = %d (%s)", recMetodo.Code, recMetodo.Body.String())
	}
	var metodoResp struct {
		Data MetodoPago `json:"data"`
	}
	_ = json.Unmarshal(recMetodo.Body.Bytes(), &metodoResp)

	// 3. Owner B intentando suplantar X-Complejo-ID de Owner A -> 403 Forbidden
	recSpoof := doReq(http.MethodGet, "/api/v1/dueno/complejo", "token-owner-b", "", map[string]string{
		"X-Complejo-ID": complejoA.String(),
	})
	if recSpoof.Code != http.StatusForbidden {
		t.Fatalf("Owner B con X-Complejo-ID ajeno debía recibir 403, obtenido: %d (%s)", recSpoof.Code, recSpoof.Body.String())
	}

	// 4. Owner B listando sus propios recursos no ve ninguno de Owner A
	recListB := doReq(http.MethodGet, "/api/v1/dueno/canchas", "token-owner-b", "", nil)
	if recListB.Code != http.StatusOK || !strings.Contains(recListB.Body.String(), `"data":[]`) {
		t.Fatalf("Owner B debía ver lista vacía de canchas, obtenido: %d (%s)", recListB.Code, recListB.Body.String())
	}

	// 5. Owner B intentando editar o eliminar recursos de Owner A -> 404 Not Found
	crossChecks := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"PATCH cancha ajena", http.MethodPatch, "/api/v1/dueno/canchas/" + canchaAID.String(), `{"nombre":"Hackeada"}`},
		{"DELETE cancha ajena", http.MethodDelete, "/api/v1/dueno/canchas/" + canchaAID.String(), ""},
		{"POST tarifa con cancha ajena", http.MethodPost, "/api/v1/dueno/tarifas", `{"cancha_id":"` + canchaAID.String() + `","dia_semana":1,"minuto_inicio":480,"minuto_fin":600,"precio_hora":50}`},
		{"PATCH tarifa ajena", http.MethodPatch, "/api/v1/dueno/tarifas/" + tarifaResp.Data.ID.String(), `{"precio_hora":1}`},
		{"DELETE tarifa ajena", http.MethodDelete, "/api/v1/dueno/tarifas/" + tarifaResp.Data.ID.String(), ""},
		{"PATCH cupón ajeno", http.MethodPatch, "/api/v1/dueno/cupones/" + cuponResp.Data.ID.String(), `{"activo":false}`},
		{"PATCH método ajeno", http.MethodPatch, "/api/v1/dueno/metodos-pago/" + metodoResp.Data.ID.String(), `{"titular":"Intruso"}`},
		{"DELETE método ajeno", http.MethodDelete, "/api/v1/dueno/metodos-pago/" + metodoResp.Data.ID.String(), ""},
	}
	for _, tc := range crossChecks {
		rec := doReq(tc.method, tc.path, "token-owner-b", tc.body, nil)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, esperado 404 (%s)", tc.name, rec.Code, rec.Body.String())
		}
	}

	// 6. Owner A intentando publicar sin completar requisitos -> 422 Unprocessable Entity
	recPub := doReq(http.MethodPost, "/api/v1/dueno/complejo/publicar", "token-owner-a", "", nil)
	if recPub.Code != http.StatusUnprocessableEntity {
		t.Fatalf("publicar incompleto debía responder 422, obtenido: %d (%s)", recPub.Code, recPub.Body.String())
	}
	if !strings.Contains(recPub.Body.String(), ReqUbicacionRequerida) || !strings.Contains(recPub.Body.String(), ReqHorarioAbiertoRequerido) {
		t.Fatalf("respuesta 422 debía detallar requisitos faltantes, obtenido: %s", recPub.Body.String())
	}
}

func TestHorariosCuponesAndTransferenciaValidations(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepo()
	duenoID, complejoID := uuid.New(), uuid.New()
	repo.seedComplejo(duenoID, complejoID, "Complejo Validaciones")
	svc := NewService(repo, mustNewTestCipher(t))

	// Horarios: menos de 7 días -> ValidationError
	_, err := svc.UpsertHorarios(ctx, complejoID, UpsertHorariosRequest{
		Horarios: []HorarioDiaInput{{DiaSemana: 1, MinutoApertura: 480, MinutoCierre: 1380}},
	})
	var valErr *ValidationError
	if !errors.As(err, &valErr) {
		t.Fatalf("horarios incompletos debían devolver ValidationError, obtenido: %v", err)
	}

	// Horarios: día abierto con apertura >= cierre -> ValidationError
	diasInvalid := make([]HorarioDiaInput, 7)
	for d := 0; d < 7; d++ {
		diasInvalid[d] = HorarioDiaInput{DiaSemana: d, MinutoApertura: 480, MinutoCierre: 1380}
	}
	diasInvalid[0] = HorarioDiaInput{DiaSemana: 0, MinutoApertura: 1000, MinutoCierre: 900, Cerrado: false}
	if _, err := svc.UpsertHorarios(ctx, complejoID, UpsertHorariosRequest{Horarios: diasInvalid}); !errors.As(err, &valErr) {
		t.Fatalf("día abierto con apertura >= cierre debía fallar, obtenido: %v", err)
	}

	// Horarios: día cerrado con 0..0 + 6 días abiertos -> válido y ordenado 0..6
	diasInvalid[0] = HorarioDiaInput{DiaSemana: 0, MinutoApertura: 0, MinutoCierre: 0, Cerrado: true}
	savedHorarios, err := svc.UpsertHorarios(ctx, complejoID, UpsertHorariosRequest{Horarios: diasInvalid})
	if err != nil || len(savedHorarios) != 7 || !savedHorarios[0].Cerrado {
		t.Fatalf("UpsertHorarios válido falló: err=%v, len=%d", err, len(savedHorarios))
	}

	// Cupones: normalización a mayúsculas, RN-09 acumulable=false por defecto y duplicado -> ErrCuponCodeAlreadyExists
	cupon, err := svc.CreateCupon(ctx, complejoID, CreateCuponRequest{
		Codigo:         "  huacho-2026 ",
		TipoDescuento:  "porcentaje",
		ValorDescuento: ptrF64(15),
	})
	if err != nil {
		t.Fatalf("CreateCupon falló: %v", err)
	}
	if cupon.Codigo != "HUACHO-2026" || cupon.AcumulableHoraMuerta {
		t.Fatalf("cupón inesperado: %+v", cupon)
	}
	if _, err := svc.CreateCupon(ctx, complejoID, CreateCuponRequest{
		Codigo:         "HUACHO-2026",
		TipoDescuento:  "MONTO_FIJO",
		ValorDescuento: ptrF64(10),
	}); !errors.Is(err, ErrCuponCodeAlreadyExists) {
		t.Fatalf("cupón duplicado debía devolver ErrCuponCodeAlreadyExists, obtenido: %v", err)
	}

	// Método TRANSFERENCIA requiere banco + cuenta + titular y rechaza credenciales de pasarela
	if _, err := svc.CreateMetodoPago(ctx, complejoID, CreateMetodoPagoRequest{
		Tipo:         "TRANSFERENCIA",
		Titular:      ptrStr("Complejo SAC"),
		Credenciales: json.RawMessage(`{"secret_key":"nope"}`),
	}); !errors.As(err, &valErr) || valErr.Fields["entidad_financiera"] == "" || valErr.Fields["numero_destino"] == "" || valErr.Fields["credenciales"] == "" {
		t.Fatalf("TRANSFERENCIA inválida debía reportar banco, cuenta y credenciales, obtenido: %v", err)
	}

	transfer, err := svc.CreateMetodoPago(ctx, complejoID, CreateMetodoPagoRequest{
		Tipo:              "TRANSFERENCIA",
		Titular:           ptrStr("Complejo SAC"),
		EntidadFinanciera: ptrStr("BCP"),
		NumeroDestino:     ptrStr("191-12345678-0-99"),
		CCI:               ptrStr("00219100123456780099"),
	})
	if err != nil || transfer.TieneCredenciales || !transfer.Activo {
		t.Fatalf("CreateMetodoPago TRANSFERENCIA falló: err=%v, mp=%+v", err, transfer)
	}
	deactivated, err := svc.DeactivateMetodoPago(ctx, complejoID, transfer.ID)
	if err != nil || deactivated.Activo {
		t.Fatalf("DeactivateMetodoPago falló: err=%v, activo=%v", err, deactivated.Activo)
	}
}
