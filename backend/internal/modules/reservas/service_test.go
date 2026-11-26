package reservas

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jdpaz-sys/futbol-digibyte-peru/backend/internal/db"
	apphttp "github.com/jdpaz-sys/futbol-digibyte-peru/backend/internal/http"
)

// ---------- Fake in-memory Store para pruebas unitarias determinísticas ----------

type fakeReservaRecord struct {
	ID              uuid.UUID
	ComplejoID      uuid.UUID
	CanchaID        uuid.UUID
	JugadorID       uuid.UUID
	Estado          string
	Inicio          time.Time
	Fin             time.Time
	CuponID         *uuid.UUID
	PrecioBase      float64
	MontoDescuento  float64
	MontoTotal      float64
	MontoAdelanto   float64
	SaldoPendiente  float64
	HoldExpiraAt    *time.Time
	PorValidarDesde *time.Time
	CodigoQR        *string
	CreatedAt       time.Time
}

type fakeAuditRecord struct {
	ReservaID      uuid.UUID
	Accion         string
	EstadoAnterior string
	EstadoNuevo    string
}

type fakeStore struct {
	mu           sync.Mutex
	now          time.Time
	complejos    map[uuid.UUID]ComplejoContext
	canchas      map[uuid.UUID]CanchaInfo
	horarios     map[int16]*HorarioAtencion
	tarifas      []TarifaFranja
	cupones      map[uuid.UUID]*CuponRecord
	metodosPago  []MetodoPagoPublico
	reservas     map[uuid.UUID]*fakeReservaRecord
	comprobantes map[uuid.UUID]ComprobanteAccessRecord
	audits       []fakeAuditRecord
	notifsCount  int
	failCompTx   error
	expireBatches []int
}

func newFakeStore(now time.Time) *fakeStore {
	return &fakeStore{
		now:          now,
		complejos:    make(map[uuid.UUID]ComplejoContext),
		canchas:      make(map[uuid.UUID]CanchaInfo),
		horarios:     make(map[int16]*HorarioAtencion),
		cupones:      make(map[uuid.UUID]*CuponRecord),
		reservas:     make(map[uuid.UUID]*fakeReservaRecord),
		comprobantes: make(map[uuid.UUID]ComprobanteAccessRecord),
	}
}

func (f *fakeStore) GetComplejoBySlug(_ context.Context, slug string) (ComplejoContext, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.complejos {
		if c.Slug == slug && c.Publicado {
			return c, nil
		}
	}
	return ComplejoContext{}, ErrComplejoNotFound
}

func (f *fakeStore) GetActiveCancha(_ context.Context, complejoID, canchaID uuid.UUID) (CanchaInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.canchas[canchaID]
	if !ok || c.ComplejoID != complejoID || !c.Activa {
		return CanchaInfo{}, ErrCanchaNotFound
	}
	return c, nil
}

func (f *fakeStore) GetHorarioByDia(_ context.Context, _ uuid.UUID, diaSemana int16) (*HorarioAtencion, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	h := f.horarios[diaSemana]
	if h == nil {
		return nil, nil
	}
	cp := *h
	return &cp, nil
}

func (f *fakeStore) ListActiveTarifasForDay(_ context.Context, _ uuid.UUID, canchaID uuid.UUID, diaSemana int16) ([]TarifaFranja, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]TarifaFranja, 0)
	for _, t := range f.tarifas {
		if t.DiaSemana != diaSemana {
			continue
		}
		if t.CanchaID == nil || *t.CanchaID == canchaID {
			out = append(out, t)
		}
	}
	return out, nil
}

func (f *fakeStore) ListOcupacionesRange(_ context.Context, canchaID uuid.UUID, rangeStart, rangeEnd, now time.Time) ([]OcupacionIntervalo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]OcupacionIntervalo, 0)
	for _, r := range f.reservas {
		if r.CanchaID != canchaID {
			continue
		}
		blocking := r.Estado == EstadoPorValidar || r.Estado == EstadoConfirmada ||
			(r.Estado == EstadoPendientePago && r.HoldExpiraAt != nil && r.HoldExpiraAt.After(now))
		if !blocking {
			continue
		}
		if r.Inicio.Before(rangeEnd) && r.Fin.After(rangeStart) {
			out = append(out, OcupacionIntervalo{Inicio: r.Inicio, Fin: r.Fin, Estado: r.Estado})
		}
	}
	return out, nil
}

func (f *fakeStore) GetCuponByCodigo(_ context.Context, complejoID uuid.UUID, codigo string) (CuponRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.cupones {
		if c.ComplejoID == complejoID && c.Codigo == codigo {
			return *c, nil
		}
	}
	return CuponRecord{}, ErrCuponNotFound
}

func (f *fakeStore) CreateHoldTx(_ context.Context, in CreateHoldTxInput) (CreateHoldTxOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	complejo, ok := f.complejos[in.ComplejoID]
	if !ok || !complejo.Publicado {
		return CreateHoldTxOutput{}, ErrComplejoNotFound
	}
	if !isSuscripcionVigente(complejo, f.now) {
		return CreateHoldTxOutput{}, ErrComplejoNoDisponible
	}

	cancha, ok := f.canchas[in.CanchaID]
	if !ok || cancha.ComplejoID != complejo.ID || !cancha.Activa {
		return CreateHoldTxOutput{}, ErrCanchaNotFound
	}

	_, diaSemana, _, _, err := ExtractRangeMinutesLima(in.Inicio, in.Fin)
	if err != nil {
		return CreateHoldTxOutput{}, err
	}

	horario := f.horarios[diaSemana]
	tarifas := make([]TarifaFranja, 0)
	for _, t := range f.tarifas {
		if t.DiaSemana == diaSemana && (t.CanchaID == nil || *t.CanchaID == cancha.ID) {
			tarifas = append(tarifas, t)
		}
	}

	var cuponPtr *CuponRecord
	if in.CodigoCupon != "" {
		for _, c := range f.cupones {
			if c.ComplejoID == complejo.ID && c.Codigo == in.CodigoCupon {
				cp := *c
				cuponPtr = &cp
				break
			}
		}
		if cuponPtr == nil {
			return CreateHoldTxOutput{}, ErrCuponNotFound
		}
	}

	pricing, err := CalculatePricing(complejo, cancha.ID, horario, tarifas, in.Inicio, in.Fin, cuponPtr, f.now)
	if err != nil {
		return CreateHoldTxOutput{}, err
	}

	// Simula restricción GIST ex_reservas_cancha_no_solape
	for _, existing := range f.reservas {
		if existing.CanchaID != cancha.ID {
			continue
		}
		if existing.Estado == EstadoPendientePago || existing.Estado == EstadoPorValidar || existing.Estado == EstadoConfirmada {
			if existing.Inicio.Before(pricing.Fin) && existing.Fin.After(pricing.Inicio) {
				return CreateHoldTxOutput{}, ErrSlotNotAvailable
			}
		}
	}

	var cuponID *uuid.UUID
	if cuponPtr != nil {
		orig := f.cupones[cuponPtr.ID]
		if orig.MaxUsos != nil && orig.UsosActuales >= *orig.MaxUsos {
			return CreateHoldTxOutput{}, ErrCuponExhausted
		}
		orig.UsosActuales++
		id := orig.ID
		cuponID = &id
	}

	reservaID := uuid.New()
	expira := f.now.Add(time.Duration(complejo.HoldDuracionMin) * time.Minute)
	f.reservas[reservaID] = &fakeReservaRecord{
		ID:             reservaID,
		ComplejoID:     complejo.ID,
		CanchaID:       cancha.ID,
		JugadorID:      in.JugadorID,
		Estado:         EstadoPendientePago,
		Inicio:         pricing.Inicio,
		Fin:            pricing.Fin,
		CuponID:        cuponID,
		PrecioBase:     pricing.PrecioBase,
		MontoDescuento: pricing.MontoDescuento,
		MontoTotal:     pricing.MontoTotal,
		MontoAdelanto:  pricing.MontoAdelanto,
		SaldoPendiente: pricing.SaldoPendiente,
		HoldExpiraAt:   &expira,
		CreatedAt:      f.now,
	}
	f.audits = append(f.audits, fakeAuditRecord{
		ReservaID:   reservaID,
		Accion:      AccionHoldCreado,
		EstadoNuevo: EstadoPendientePago,
	})

	return CreateHoldTxOutput{
		Reserva: ReservaResumen{
			ID:                  reservaID,
			ComplejoID:          complejo.ID,
			ComplejoNombre:      complejo.Nombre,
			CanchaID:            cancha.ID,
			CanchaNombre:        cancha.Nombre,
			JugadorID:           in.JugadorID,
			Origen:              OrigenApp,
			Estado:              EstadoPendientePago,
			Inicio:              pricing.Inicio,
			Fin:                 pricing.Fin,
			DuracionMin:         pricing.DuracionMin,
			CuponID:             cuponID,
			PrecioBase:          pricing.PrecioBase,
			MontoDescuento:      pricing.MontoDescuento,
			MontoTotal:          pricing.MontoTotal,
			MontoAdelanto:       pricing.MontoAdelanto,
			SaldoPendiente:      pricing.SaldoPendiente,
			TerminosAceptadosAt: f.now,
			HoldExpiraAt:        expira,
			CreatedAt:           f.now,
		},
		Pricing:     pricing,
		MetodosPago: f.metodosPago,
		ServerNow:   f.now,
	}, nil
}

func (f *fakeStore) PrecheckUploadComprobante(_ context.Context, reservaID, jugadorID, metodoPagoID uuid.UUID) (uuid.UUID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	res, ok := f.reservas[reservaID]
	if !ok {
		return uuid.Nil, ErrReservaNotFound
	}
	if res.JugadorID != jugadorID {
		return uuid.Nil, ErrReservaForbidden
	}
	if err := validateHoldAcceptsComprobante(res.Estado, res.HoldExpiraAt, f.now); err != nil {
		return uuid.Nil, err
	}
	foundMetodo := false
	for _, m := range f.metodosPago {
		if m.ID == metodoPagoID {
			if m.Tipo != MetodoPagoYape && m.Tipo != MetodoPagoPlin && m.Tipo != MetodoPagoTransferencia {
				return uuid.Nil, ErrMetodoPagoNoManual
			}
			foundMetodo = true
			break
		}
	}
	if !foundMetodo {
		return uuid.Nil, ErrMetodoPagoNotFound
	}
	return res.ComplejoID, nil
}

func (f *fakeStore) RegisterComprobanteTx(_ context.Context, params RegisterComprobanteParams) (UploadComprobanteResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.failCompTx != nil {
		return UploadComprobanteResponse{}, f.failCompTx
	}

	res, ok := f.reservas[params.ReservaID]
	if !ok {
		return UploadComprobanteResponse{}, ErrReservaNotFound
	}
	if res.JugadorID != params.JugadorID {
		return UploadComprobanteResponse{}, ErrReservaForbidden
	}
	if err := validateHoldAcceptsComprobante(res.Estado, res.HoldExpiraAt, f.now); err != nil {
		return UploadComprobanteResponse{}, err
	}

	complejo := f.complejos[res.ComplejoID]
	porValidar := f.now
	res.Estado = EstadoPorValidar
	res.PorValidarDesde = &porValidar

	key := params.EvidenciaStorageKey
	jid := params.JugadorID
	f.comprobantes[params.ComprobanteID] = ComprobanteAccessRecord{
		ID:                  params.ComprobanteID,
		ReservaID:           params.ReservaID,
		ComplejoID:          res.ComplejoID,
		JugadorID:           &jid,
		DuenoID:             complejo.DuenoID,
		EvidenciaStorageKey: &key,
	}
	f.audits = append(f.audits, fakeAuditRecord{
		ReservaID:      params.ReservaID,
		Accion:         AccionComprobanteSubido,
		EstadoAnterior: EstadoPendientePago,
		EstadoNuevo:    EstadoPorValidar,
	})
	f.notifsCount++

	cod := params.CodigoOperacion
	ep := BuildEvidenciaEndpoint(params.ComprobanteID)
	return UploadComprobanteResponse{
		ReservaID:       params.ReservaID,
		EstadoReserva:   EstadoPorValidar,
		PorValidarDesde: porValidar,
		Comprobante: ComprobanteInfo{
			ID:                params.ComprobanteID,
			ReservaID:         params.ReservaID,
			MetodoPagoID:      params.MetodoPagoID,
			MetodoPagoTipo:    MetodoPagoYape,
			EsAutomatico:      false,
			EstadoRevision:    RevisionPendiente,
			MontoDeclarado:    params.MontoDeclarado,
			CodigoOperacion:   &cod,
			EvidenciaEndpoint: &ep,
			CreatedAt:         f.now,
		},
	}, nil
}

func (f *fakeStore) ListJugadorReservas(_ context.Context, jugadorID uuid.UUID, filter ListJugadorReservasFilter) ([]JugadorReservaItem, int, time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	items := make([]JugadorReservaItem, 0)
	for _, r := range f.reservas {
		if r.JugadorID != jugadorID {
			continue
		}
		if filter.Estado != "" && r.Estado != filter.Estado {
			continue
		}
		comp := f.complejos[r.ComplejoID]
		cancha := f.canchas[r.CanchaID]
		items = append(items, JugadorReservaItem{
			ID:              r.ID,
			ComplejoID:      r.ComplejoID,
			ComplejoSlug:    comp.Slug,
			ComplejoNombre:  comp.Nombre,
			CanchaID:        r.CanchaID,
			CanchaNombre:    cancha.Nombre,
			CanchaModalidad: cancha.Modalidad,
			Estado:          r.Estado,
			Origen:          OrigenApp,
			Inicio:          r.Inicio,
			Fin:             r.Fin,
			DuracionMin:     int(r.Fin.Sub(r.Inicio) / time.Minute),
			MontoTotal:      r.MontoTotal,
			MontoAdelanto:   r.MontoAdelanto,
			SaldoPendiente:  r.SaldoPendiente,
			HoldExpiraAt:    r.HoldExpiraAt,
			PorValidarDesde: r.PorValidarDesde,
			CodigoQR:        r.CodigoQR,
			CreatedAt:       r.CreatedAt,
		})
	}
	return items, len(items), f.now, nil
}

func (f *fakeStore) GetJugadorReservaDetail(_ context.Context, reservaID uuid.UUID) (JugadorReservaDetail, time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.reservas[reservaID]
	if !ok {
		return JugadorReservaDetail{}, time.Time{}, ErrReservaNotFound
	}
	comp := f.complejos[r.ComplejoID]
	cancha := f.canchas[r.CanchaID]
	lat := -11.1067
	lng := -77.6050
	dir := "Av. 28 de Julio 450, Huacho"
	ref := "Frente al estadio Segundo Aranda"

	return JugadorReservaDetail{
		ID:             r.ID,
		JugadorID:      r.JugadorID,
		Estado:         r.Estado,
		Origen:         OrigenApp,
		Inicio:         r.Inicio,
		Fin:            r.Fin,
		DuracionMin:    int(r.Fin.Sub(r.Inicio) / time.Minute),
		PrecioBase:     r.PrecioBase,
		MontoDescuento: r.MontoDescuento,
		MontoTotal:     r.MontoTotal,
		MontoAdelanto:  r.MontoAdelanto,
		SaldoPendiente: r.SaldoPendiente,
		HoldExpiraAt:   r.HoldExpiraAt,
		CodigoQR:       r.CodigoQR,
		Complejo: ComplejoRutaInfo{
			ID:               comp.ID,
			Slug:             comp.Slug,
			Nombre:           comp.Nombre,
			DireccionEscrita: &dir,
			Referencias:      &ref,
			Distrito:         "Huacho",
			Provincia:        "Huaura",
			Departamento:     "Lima",
			Lat:              &lat,
			Lng:              &lng,
		},
		Cancha:       cancha,
		Comprobantes: []ComprobanteInfo{},
		CreatedAt:    r.CreatedAt,
		UpdatedAt:    r.CreatedAt,
	}, f.now, nil
}

func (f *fakeStore) GetComprobanteAccess(_ context.Context, comprobanteID uuid.UUID) (ComprobanteAccessRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec, ok := f.comprobantes[comprobanteID]
	if !ok {
		return ComprobanteAccessRecord{}, ErrComprobanteNotFound
	}
	return rec, nil
}

func (f *fakeStore) ExpireStaleHolds(_ context.Context, batchSize int) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	expired := 0
	for _, r := range f.reservas {
		if expired >= batchSize {
			break
		}
		if r.Estado == EstadoPendientePago && r.HoldExpiraAt != nil && !r.HoldExpiraAt.After(f.now) {
			r.Estado = EstadoExpirada
			if r.CuponID != nil {
				if c, ok := f.cupones[*r.CuponID]; ok && c.UsosActuales > 0 {
					c.UsosActuales--
				}
			}
			f.audits = append(f.audits, fakeAuditRecord{
				ReservaID:      r.ID,
				Accion:         AccionHoldExpirado,
				EstadoAnterior: EstadoPendientePago,
				EstadoNuevo:    EstadoExpirada,
			})
			expired++
		}
	}
	f.expireBatches = append(f.expireBatches, expired)
	return expired, nil
}

// ---------- Helpers de imágenes de prueba ----------

func makeValidPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	img.Set(0, 0, color.RGBA{R: 37, G: 99, B: 235, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("error codificando PNG de prueba: %v", err)
	}
	return buf.Bytes()
}

func makeValidJPEG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("error codificando JPEG de prueba: %v", err)
	}
	return buf.Bytes()
}

func makeValidWebPHeader() []byte {
	// Cabecera mínima RIFF + tamaño + WEBP + VP8
	return []byte{'R', 'I', 'F', 'F', 0x10, 0x00, 0x00, 0x00, 'W', 'E', 'B', 'P', 'V', 'P', '8', ' ', 0x00, 0x00, 0x00, 0x00}
}

func ptrStr(s string) *string { return &s }
func ptrInt(i int) *int       { return &i }

func setupBaseFixture(t *testing.T) (*fakeStore, ComplejoContext, CanchaInfo, uuid.UUID, time.Time) {
	t.Helper()
	loc := LimaLocation()
	// Jueves 2026-11-26 10:00:00 America/Lima
	now := time.Date(2026, 11, 26, 10, 0, 0, 0, loc)
	vence := now.Add(30 * 24 * time.Hour)
	terminos := "Adelanto no reembolsable fuera de ventana de 24 horas."

	duenoID := uuid.New()
	complejoID := uuid.New()
	canchaID := uuid.New()

	complejo := ComplejoContext{
		ID:                  complejoID,
		DuenoID:             duenoID,
		Slug:                "la-bombonera-huacho",
		Nombre:              "La Bombonera Huacho",
		Publicado:           true,
		UnidadReservaMin:    60,
		HoldDuracionMin:     10,
		TipoAdelanto:        TipoPorcentaje,
		ValorAdelanto:       50.00,
		HorasMinCancelacion: 24,
		TerminosReserva:     &terminos,
		EstadoSuscripcion:   EstadoSuscripcionActiva,
		SuscripcionVenceAt:  &vence,
	}

	cancha := CanchaInfo{
		ID:          canchaID,
		ComplejoID:  complejoID,
		Nombre:      "Cancha 7v7 Norte",
		Modalidad:   "7v7",
		Superficie:  "SINTETICO",
		Techada:     false,
		Iluminacion: true,
		Activa:      true,
	}

	store := newFakeStore(now)
	store.complejos[complejoID] = complejo
	store.canchas[canchaID] = cancha

	diaJueves := int16(now.Weekday()) // 4 = Jueves
	store.horarios[diaJueves] = &HorarioAtencion{
		DiaSemana:      diaJueves,
		MinutoApertura: 480,  // 08:00
		MinutoCierre:   1380, // 23:00
		Cerrado:        false,
	}

	// Tarifas generales: Día 08:00-18:00 (S/ 60), Noche 18:00-23:00 (S/ 90)
	store.tarifas = []TarifaFranja{
		{
			ID:           uuid.New(),
			CanchaID:     nil,
			DiaSemana:    diaJueves,
			MinutoInicio: 480,
			MinutoFin:    1080,
			PrecioHora:   60.00,
			Etiqueta:     ptrStr("Tarifa Día"),
			EsHoraMuerta: false,
		},
		{
			ID:           uuid.New(),
			CanchaID:     nil,
			DiaSemana:    diaJueves,
			MinutoInicio: 1080,
			MinutoFin:    1380,
			PrecioHora:   90.00,
			Etiqueta:     ptrStr("Tarifa Noche"),
			EsHoraMuerta: false,
		},
	}

	yapeID := uuid.New()
	store.metodosPago = []MetodoPagoPublico{
		{
			ID:            yapeID,
			Tipo:          MetodoPagoYape,
			Titular:       ptrStr("La Bombonera SAC"),
			NumeroDestino: ptrStr("999888777"),
		},
	}

	return store, complejo, cancha, yapeID, now
}

// ---------- PRUEBA 1: Franja tarifaria mixta día + noche (RN-08) ----------

func TestPricingMixedTarifaDiaYNoche(t *testing.T) {
	store, complejo, cancha, _, now := setupBaseFixture(t)
	loc := LimaLocation()

	svc := NewService(store, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.now = func() time.Time { return now }

	// Bloque mixto 17:00 - 19:00 -> 1h día (60) + 1h noche (90) = 150.00
	resp, err := svc.Cotizar(context.Background(), complejo.Slug, CotizarRequest{
		CanchaID: cancha.ID.String(),
		Inicio:   time.Date(2026, 11, 26, 17, 0, 0, 0, loc).Format(time.RFC3339),
		Fin:      time.Date(2026, 11, 26, 19, 0, 0, 0, loc).Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("Cotizar falló inesperadamente: %v", err)
	}

	if resp.PrecioBase != 150.00 {
		t.Errorf("PrecioBase = %.2f, esperado 150.00", resp.PrecioBase)
	}
	if resp.MontoTotal != 150.00 {
		t.Errorf("MontoTotal = %.2f, esperado 150.00", resp.MontoTotal)
	}
	if resp.MontoAdelanto != 75.00 || resp.SaldoPendiente != 75.00 {
		t.Errorf("Adelanto=%.2f Saldo=%.2f, esperado 75.00 / 75.00", resp.MontoAdelanto, resp.SaldoPendiente)
	}
	if len(resp.Desglose) != 2 {
		t.Fatalf("len(Desglose) = %d, esperado 2 tramos", len(resp.Desglose))
	}
	if resp.Desglose[0].HoraInicio != "17:00" || resp.Desglose[0].HoraFin != "18:00" || resp.Desglose[0].Subtotal != 60.00 {
		t.Errorf("tramo 0 inesperado: %+v", resp.Desglose[0])
	}
	if resp.Desglose[1].HoraInicio != "18:00" || resp.Desglose[1].HoraFin != "19:00" || resp.Desglose[1].Subtotal != 90.00 {
		t.Errorf("tramo 1 inesperado: %+v", resp.Desglose[1])
	}
	if resp.TerminosReserva == "" {
		t.Error("TerminosReserva no debe estar vacío")
	}

	// Caso adicional con unidad_reserva_min = 30 min: 17:30 - 18:30 -> 30m*60 (30) + 30m*90 (45) = 75.00
	complejo.UnidadReservaMin = 30
	store.complejos[complejo.ID] = complejo
	resp30, err := svc.Cotizar(context.Background(), complejo.Slug, CotizarRequest{
		CanchaID: cancha.ID.String(),
		Inicio:   time.Date(2026, 11, 26, 17, 30, 0, 0, loc).Format(time.RFC3339),
		Fin:      time.Date(2026, 11, 26, 18, 30, 0, 0, loc).Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("Cotizar 30min mixto falló: %v", err)
	}
	if resp30.PrecioBase != 75.00 {
		t.Errorf("PrecioBase 30min mixto = %.2f, esperado 75.00", resp30.PrecioBase)
	}
}

// ---------- PRUEBA 2: Prioridad tarifa específica de cancha sobre tarifa general ----------

func TestPricingCanchaSpecificOverridesGeneral(t *testing.T) {
	store, complejo, cancha, _, now := setupBaseFixture(t)
	loc := LimaLocation()
	diaJueves := int16(now.Weekday())

	// Agregamos tarifa específica de esta cancha solo para 18:00-23:00 (S/ 110 en lugar de S/ 90 general)
	cID := cancha.ID
	store.tarifas = append(store.tarifas, TarifaFranja{
		ID:           uuid.New(),
		CanchaID:     &cID,
		DiaSemana:    diaJueves,
		MinutoInicio: 1080,
		MinutoFin:    1380,
		PrecioHora:   110.00,
		Etiqueta:     ptrStr("Noche VIP Cancha 7v7"),
		EsHoraMuerta: false,
	})

	svc := NewService(store, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.now = func() time.Time { return now }

	// 17:00-19:00: 17:00-18:00 usa general (60) + 18:00-19:00 usa específica de cancha (110) = 170.00
	resp, err := svc.Cotizar(context.Background(), complejo.Slug, CotizarRequest{
		CanchaID: cancha.ID.String(),
		Inicio:   time.Date(2026, 11, 26, 17, 0, 0, 0, loc).Format(time.RFC3339),
		Fin:      time.Date(2026, 11, 26, 19, 0, 0, 0, loc).Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("Cotizar con override de cancha falló: %v", err)
	}
	if resp.PrecioBase != 170.00 {
		t.Fatalf("PrecioBase = %.2f, esperado 170.00 (60 general + 110 específica)", resp.PrecioBase)
	}
	if resp.Desglose[0].EsTarifaCancha {
		t.Error("tramo 17:00-18:00 debería ser tarifa general")
	}
	if !resp.Desglose[1].EsTarifaCancha {
		t.Error("tramo 18:00-19:00 debería priorizar tarifa específica de cancha")
	}

	// Verificar también en disponibilidad en vivo que el slot 18:00-19:00 muestre S/ 110 y pasados estén bloqueados
	disp, err := svc.GetDisponibilidad(context.Background(), complejo.Slug, cancha.ID, "2026-11-26")
	if err != nil {
		t.Fatalf("GetDisponibilidad falló: %v", err)
	}
	var found09, found18 *DisponibilidadSlot
	for i := range disp.Slots {
		switch disp.Slots[i].HoraInicio {
		case "09:00":
			found09 = &disp.Slots[i]
		case "18:00":
			found18 = &disp.Slots[i]
		}
	}
	if found09 == nil || found09.Disponible || found09.MotivoNoDisponible == nil || *found09.MotivoNoDisponible != MotivoSlotPasado {
		t.Errorf("slot 09:00 (now=10:00) debía estar bloqueado por PASADO, obtenido: %+v", found09)
	}
	if found18 == nil || !found18.Disponible || found18.Precio == nil || *found18.Precio != 110.00 {
		t.Errorf("slot 18:00 debía estar disponible a S/ 110.00, obtenido: %+v", found18)
	}
}

// ---------- PRUEBA 3: Regla RN-09 Cupón vs Hora Muerta ----------

func TestPricingCouponVsHoraMuertaRN09(t *testing.T) {
	store, complejo, cancha, _, now := setupBaseFixture(t)
	loc := LimaLocation()

	// Marcamos la tarifa diurna (08:00-18:00) como hora muerta
	store.tarifas[0].EsHoraMuerta = true

	cuponNoAcum := &CuponRecord{
		ID:                   uuid.New(),
		ComplejoID:           complejo.ID,
		Codigo:               "NOACUM10",
		TipoDescuento:        TipoPorcentaje,
		ValorDescuento:       10.00,
		ValidoDesde:          now.Add(-time.Hour),
		MaxUsos:              ptrInt(10),
		UsosActuales:         0,
		AcumulableHoraMuerta: false,
		Activo:               true,
	}
	cuponAcum := &CuponRecord{
		ID:                   uuid.New(),
		ComplejoID:           complejo.ID,
		Codigo:               "ACUM20",
		TipoDescuento:        TipoMontoFijo,
		ValorDescuento:       20.00,
		ValidoDesde:          now.Add(-time.Hour),
		AcumulableHoraMuerta: true,
		Activo:               true,
	}
	store.cupones[cuponNoAcum.ID] = cuponNoAcum
	store.cupones[cuponAcum.ID] = cuponAcum

	svc := NewService(store, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.now = func() time.Time { return now }

	// 17:00-19:00 incluye 1h en hora muerta (17-18) -> cupón NO acumulable debe rechazarse con ErrCuponNotStackableDeadHour
	_, err := svc.Cotizar(context.Background(), complejo.Slug, CotizarRequest{
		CanchaID:    cancha.ID.String(),
		Inicio:      time.Date(2026, 11, 26, 17, 0, 0, 0, loc).Format(time.RFC3339),
		Fin:         time.Date(2026, 11, 26, 19, 0, 0, 0, loc).Format(time.RFC3339),
		CodigoCupon: "noacum10",
	})
	if !errors.Is(err, ErrCuponNotStackableDeadHour) {
		t.Fatalf("esperado ErrCuponNotStackableDeadHour por RN-09, obtenido: %v", err)
	}

	// Mismo cupón NO acumulable en horario 18:00-20:00 (sin hora muerta) -> SÍ debe aplicar (10% de 180 = 18 -> 162)
	respNoche, err := svc.Cotizar(context.Background(), complejo.Slug, CotizarRequest{
		CanchaID:    cancha.ID.String(),
		Inicio:      time.Date(2026, 11, 26, 18, 0, 0, 0, loc).Format(time.RFC3339),
		Fin:         time.Date(2026, 11, 26, 20, 0, 0, 0, loc).Format(time.RFC3339),
		CodigoCupon: "NOACUM10",
	})
	if err != nil {
		t.Fatalf("cupón sobre horario sin hora muerta debía aplicar: %v", err)
	}
	if respNoche.PrecioBase != 180.00 || respNoche.MontoDescuento != 18.00 || respNoche.MontoTotal != 162.00 {
		t.Errorf("montos inesperados: base=%.2f desc=%.2f total=%.2f", respNoche.PrecioBase, respNoche.MontoDescuento, respNoche.MontoTotal)
	}

	// Cupón CON acumulable_hora_muerta = true en 17:00-19:00 -> SÍ aplica (150 - 20 = 130)
	respAcum, err := svc.Cotizar(context.Background(), complejo.Slug, CotizarRequest{
		CanchaID:    cancha.ID.String(),
		Inicio:      time.Date(2026, 11, 26, 17, 0, 0, 0, loc).Format(time.RFC3339),
		Fin:         time.Date(2026, 11, 26, 19, 0, 0, 0, loc).Format(time.RFC3339),
		CodigoCupon: "ACUM20",
	})
	if err != nil {
		t.Fatalf("cupón acumulable en hora muerta debía aplicar: %v", err)
	}
	if respAcum.MontoDescuento != 20.00 || respAcum.MontoTotal != 130.00 {
		t.Errorf("montos con cupón acumulable inesperados: desc=%.2f total=%.2f", respAcum.MontoDescuento, respAcum.MontoTotal)
	}
}

// ---------- PRUEBA 4: Adelanto PORCENTAJE y MONTO_FIJO (incluyendo tope) ----------

func TestPricingAdelantoPorcentajeYMontoFijo(t *testing.T) {
	store, complejo, cancha, _, now := setupBaseFixture(t)
	loc := LimaLocation()
	svc := NewService(store, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.now = func() time.Time { return now }

	// Caso A: PORCENTAJE 30% sobre S/ 150.00 -> adelanto 45.00, saldo 105.00
	complejo.TipoAdelanto = TipoPorcentaje
	complejo.ValorAdelanto = 30.00
	store.complejos[complejo.ID] = complejo

	resPct, err := svc.Cotizar(context.Background(), complejo.Slug, CotizarRequest{
		CanchaID: cancha.ID.String(),
		Inicio:   time.Date(2026, 11, 26, 17, 0, 0, 0, loc).Format(time.RFC3339),
		Fin:      time.Date(2026, 11, 26, 19, 0, 0, 0, loc).Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("Cotizar PORCENTAJE falló: %v", err)
	}
	if resPct.MontoAdelanto != 45.00 || resPct.SaldoPendiente != 105.00 {
		t.Errorf("PORCENTAJE 30%%: adelanto=%.2f saldo=%.2f, esperado 45.00 / 105.00", resPct.MontoAdelanto, resPct.SaldoPendiente)
	}

	// Caso B: MONTO_FIJO S/ 40.00 sobre S/ 150.00 -> adelanto 40.00, saldo 110.00
	complejo.TipoAdelanto = TipoMontoFijo
	complejo.ValorAdelanto = 40.00
	store.complejos[complejo.ID] = complejo

	resFijo, err := svc.Cotizar(context.Background(), complejo.Slug, CotizarRequest{
		CanchaID: cancha.ID.String(),
		Inicio:   time.Date(2026, 11, 26, 17, 0, 0, 0, loc).Format(time.RFC3339),
		Fin:      time.Date(2026, 11, 26, 19, 0, 0, 0, loc).Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("Cotizar MONTO_FIJO falló: %v", err)
	}
	if resFijo.MontoAdelanto != 40.00 || resFijo.SaldoPendiente != 110.00 {
		t.Errorf("MONTO_FIJO 40: adelanto=%.2f saldo=%.2f, esperado 40.00 / 110.00", resFijo.MontoAdelanto, resFijo.SaldoPendiente)
	}

	// Caso C: MONTO_FIJO S/ 80.00 sobre 1h de S/ 60.00 -> adelanto topeado en 60.00, saldo 0.00
	complejo.ValorAdelanto = 80.00
	store.complejos[complejo.ID] = complejo

	resCap, err := svc.Cotizar(context.Background(), complejo.Slug, CotizarRequest{
		CanchaID: cancha.ID.String(),
		Inicio:   time.Date(2026, 11, 26, 15, 0, 0, 0, loc).Format(time.RFC3339),
		Fin:      time.Date(2026, 11, 26, 16, 0, 0, 0, loc).Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("Cotizar MONTO_FIJO topeado falló: %v", err)
	}
	if resCap.MontoAdelanto != 60.00 || resCap.SaldoPendiente != 0.00 {
		t.Errorf("MONTO_FIJO topeado: adelanto=%.2f saldo=%.2f, esperado 60.00 / 0.00", resCap.MontoAdelanto, resCap.SaldoPendiente)
	}
}

// ---------- PRUEBA 5: Rechazo sin aceptar términos (Decisión 2) y conflicto de solapamiento ----------

func TestCreateReservaRequiresTermsAndDetectsOverlap(t *testing.T) {
	store, complejo, cancha, _, now := setupBaseFixture(t)
	loc := LimaLocation()
	jugador1 := uuid.New()
	jugador2 := uuid.New()

	svc := NewService(store, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	handler := NewHandler(svc, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	router := apphttp.NewRouter(apphttp.RouterConfig{
		CORSAllowedOrigins: []string{"http://localhost:3000"},
		Logger:             slog.New(slog.NewTextHandler(io.Discard, nil)),
		TokenParser: func(token string) (apphttp.AuthUser, error) {
			switch token {
			case "tok-j1":
				return apphttp.AuthUser{UserID: jugador1, Rol: apphttp.RoleJugador}, nil
			case "tok-j2":
				return apphttp.AuthUser{UserID: jugador2, Rol: apphttp.RoleJugador}, nil
			default:
				return apphttp.AuthUser{}, errors.New("invalid")
			}
		},
		MountJugador: handler.RegisterJugadorRoutes,
	})

	// 1) acepta_terminos = false -> 400 Bad Request
	bodyNoTerms, _ := json.Marshal(CreateReservaRequest{
		ComplejoID:     complejo.ID.String(),
		CanchaID:       cancha.ID.String(),
		Inicio:         time.Date(2026, 11, 26, 17, 0, 0, 0, loc).Format(time.RFC3339),
		Fin:            time.Date(2026, 11, 26, 19, 0, 0, 0, loc).Format(time.RFC3339),
		AceptaTerminos: false,
	})
	req1 := httptest.NewRequest(http.MethodPost, "/api/v1/jugador/reservas", bytes.NewReader(bodyNoTerms))
	req1.Header.Set("Authorization", "Bearer tok-j1")
	req1.Header.Set("Content-Type", "application/json")
	rec1 := httptest.NewRecorder()
	router.ServeHTTP(rec1, req1)

	if rec1.Code != http.StatusBadRequest {
		t.Fatalf("sin aceptar términos esperado 400, obtenido %d: %s", rec1.Code, rec1.Body.String())
	}

	// 2) Jugador 1 acepta términos -> 201 Created
	bodyOK, _ := json.Marshal(CreateReservaRequest{
		ComplejoID:     complejo.ID.String(),
		CanchaID:       cancha.ID.String(),
		Inicio:         time.Date(2026, 11, 26, 17, 0, 0, 0, loc).Format(time.RFC3339),
		Fin:            time.Date(2026, 11, 26, 19, 0, 0, 0, loc).Format(time.RFC3339),
		AceptaTerminos: true,
	})
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/jugador/reservas", bytes.NewReader(bodyOK))
	req2.Header.Set("Authorization", "Bearer tok-j1")
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	router.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusCreated {
		t.Fatalf("creación de hold esperado 201, obtenido %d: %s", rec2.Code, rec2.Body.String())
	}

	// 3) Jugador 2 intenta horario solapado 18:00-19:00 -> 409 Conflict (SLOT_NOT_AVAILABLE)
	bodyOverlap, _ := json.Marshal(CreateReservaRequest{
		ComplejoID:     complejo.ID.String(),
		CanchaID:       cancha.ID.String(),
		Inicio:         time.Date(2026, 11, 26, 18, 0, 0, 0, loc).Format(time.RFC3339),
		Fin:            time.Date(2026, 11, 26, 19, 0, 0, 0, loc).Format(time.RFC3339),
		AceptaTerminos: true,
	})
	req3 := httptest.NewRequest(http.MethodPost, "/api/v1/jugador/reservas", bytes.NewReader(bodyOverlap))
	req3.Header.Set("Authorization", "Bearer tok-j2")
	req3.Header.Set("Content-Type", "application/json")
	rec3 := httptest.NewRecorder()
	router.ServeHTTP(rec3, req3)

	if rec3.Code != http.StatusConflict {
		t.Fatalf("solapamiento esperado 409, obtenido %d: %s", rec3.Code, rec3.Body.String())
	}
	var errResp apphttp.ErrorResponse
	if err := json.Unmarshal(rec3.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("error decodificando respuesta 409: %v", err)
	}
	if errResp.Error.Code != "SLOT_NOT_AVAILABLE" {
		t.Errorf("code = %q, esperado SLOT_NOT_AVAILABLE", errResp.Error.Code)
	}
	_ = now
}

// ---------- PRUEBA 6: Worker expira PENDIENTE_PAGO vencidos sin tocar POR_VALIDAR ----------

func TestExpirationWorkerExpiresOnlyStalePendientePago(t *testing.T) {
	store, complejo, cancha, _, now := setupBaseFixture(t)
	loc := LimaLocation()

	cuponID := uuid.New()
	store.cupones[cuponID] = &CuponRecord{
		ID:             cuponID,
		ComplejoID:     complejo.ID,
		Codigo:         "PROMO10",
		TipoDescuento:  TipoPorcentaje,
		ValorDescuento: 10,
		ValidoDesde:    now.Add(-time.Hour),
		MaxUsos:        ptrInt(5),
		UsosActuales:   2,
		Activo:         true,
	}

	expiredAt := now.Add(-2 * time.Minute)
	futureAt := now.Add(8 * time.Minute)
	porValidarAt := now.Add(-45 * time.Minute)

	idExpired := uuid.New()
	idActiveHold := uuid.New()
	idPorValidar := uuid.New()

	store.reservas[idExpired] = &fakeReservaRecord{
		ID:           idExpired,
		ComplejoID:   complejo.ID,
		CanchaID:     cancha.ID,
		JugadorID:    uuid.New(),
		Estado:       EstadoPendientePago,
		Inicio:       time.Date(2026, 11, 26, 15, 0, 0, 0, loc),
		Fin:          time.Date(2026, 11, 26, 16, 0, 0, 0, loc),
		CuponID:      &cuponID,
		HoldExpiraAt: &expiredAt,
	}
	store.reservas[idActiveHold] = &fakeReservaRecord{
		ID:           idActiveHold,
		ComplejoID:   complejo.ID,
		CanchaID:     cancha.ID,
		JugadorID:    uuid.New(),
		Estado:       EstadoPendientePago,
		Inicio:       time.Date(2026, 11, 26, 16, 0, 0, 0, loc),
		Fin:          time.Date(2026, 11, 26, 17, 0, 0, 0, loc),
		HoldExpiraAt: &futureAt,
	}
	// POR_VALIDAR con hold_expira_at en el pasado: JAMÁS debe expirar (Decisión 3)
	store.reservas[idPorValidar] = &fakeReservaRecord{
		ID:              idPorValidar,
		ComplejoID:      complejo.ID,
		CanchaID:        cancha.ID,
		JugadorID:       uuid.New(),
		Estado:          EstadoPorValidar,
		Inicio:          time.Date(2026, 11, 26, 17, 0, 0, 0, loc),
		Fin:             time.Date(2026, 11, 26, 18, 0, 0, 0, loc),
		HoldExpiraAt:    &expiredAt,
		PorValidarDesde: &porValidarAt,
	}

	worker := NewExpirationWorker(store, 20*time.Millisecond, slog.New(slog.NewTextHandler(io.Discard, nil)))
	count, err := worker.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("worker.RunOnce falló: %v", err)
	}
	if count != 1 {
		t.Fatalf("expired count = %d, esperado 1", count)
	}

	if store.reservas[idExpired].Estado != EstadoExpirada {
		t.Errorf("reserva vencida debería estar EXPIRADA, estado=%s", store.reservas[idExpired].Estado)
	}
	if store.reservas[idActiveHold].Estado != EstadoPendientePago {
		t.Errorf("hold vigente no debía expirar, estado=%s", store.reservas[idActiveHold].Estado)
	}
	if store.reservas[idPorValidar].Estado != EstadoPorValidar {
		t.Errorf("reserva POR_VALIDAR jamás debe expirar (Decisión 3), estado=%s", store.reservas[idPorValidar].Estado)
	}
	if store.cupones[cuponID].UsosActuales != 1 {
		t.Errorf("usos_actuales del cupón debía revertirse de 2 a 1, obtenido %d", store.cupones[cuponID].UsosActuales)
	}

	// Verifica arranque y apagado limpio del bucle Start(ctx) por cancelación de context
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		worker.Start(ctx)
	}()
	time.Sleep(45 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker no finalizó limpiamente tras cancelar context")
	}
}

// ---------- PRUEBA 7: Subida de comprobante, validación MIME/tamaño y control de acceso a evidencia ----------

func TestUploadComprobanteAndEvidenciaAccessControl(t *testing.T) {
	store, complejo, cancha, yapeID, now := setupBaseFixture(t)
	loc := LimaLocation()

	storage, err := db.NewLocalFileStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalFileStorage falló: %v", err)
	}

	// Verificar protección contra path traversal en LocalFileStorage
	if err := storage.Save(context.Background(), "../escape.png", "image/png", bytes.NewReader([]byte("x"))); !errors.Is(err, db.ErrInvalidStorageKey) {
		t.Fatalf("LocalFileStorage debía rechazar path traversal, obtenido: %v", err)
	}

	jugador1 := uuid.New()
	jugador2 := uuid.New()
	otroDueno := uuid.New()
	superAdmin := uuid.New()

	svc := NewService(store, storage, slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.now = func() time.Time { return now }
	handler := NewHandler(svc, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	router := apphttp.NewRouter(apphttp.RouterConfig{
		CORSAllowedOrigins: []string{"http://localhost:3000"},
		Logger:             slog.New(slog.NewTextHandler(io.Discard, nil)),
		TokenParser: func(token string) (apphttp.AuthUser, error) {
			switch token {
			case "j1":
				return apphttp.AuthUser{UserID: jugador1, Rol: apphttp.RoleJugador}, nil
			case "j2":
				return apphttp.AuthUser{UserID: jugador2, Rol: apphttp.RoleJugador}, nil
			case "dueno":
				cID := complejo.ID
				return apphttp.AuthUser{UserID: complejo.DuenoID, Rol: apphttp.RoleDueno, ComplejoID: &cID}, nil
			case "otro-dueno":
				otherCID := uuid.New()
				return apphttp.AuthUser{UserID: otroDueno, Rol: apphttp.RoleDueno, ComplejoID: &otherCID}, nil
			case "admin":
				return apphttp.AuthUser{UserID: superAdmin, Rol: apphttp.RoleSuperAdmin}, nil
			default:
				return apphttp.AuthUser{}, errors.New("invalid")
			}
		},
		MountJugador:       handler.RegisterJugadorRoutes,
		MountAuthenticated: handler.RegisterAuthenticatedRoutes,
	})

	// Creamos reserva Hold para jugador1
	holdRes, err := svc.CreateReserva(context.Background(), jugador1, CreateReservaRequest{
		ComplejoID:     complejo.ID.String(),
		CanchaID:       cancha.ID.String(),
		Inicio:         time.Date(2026, 11, 26, 17, 0, 0, 0, loc).Format(time.RFC3339),
		Fin:            time.Date(2026, 11, 26, 19, 0, 0, 0, loc).Format(time.RFC3339),
		AceptaTerminos: true,
	})
	if err != nil {
		t.Fatalf("CreateReserva falló: %v", err)
	}
	reservaID := holdRes.Reserva.ID

	// 1) Archivo falso (texto con extensión .png) debe rechazarse con 400 INVALID_FILE_TYPE
	fakeReq := buildMultipartComprobanteRequest(t, reservaID, yapeID, "OP-FAKE", "75.00", "voucher.png", []byte("no soy una imagen png real"))
	fakeReq.Header.Set("Authorization", "Bearer j1")
	fakeRec := httptest.NewRecorder()
	router.ServeHTTP(fakeRec, fakeReq)
	if fakeRec.Code != http.StatusBadRequest {
		t.Fatalf("MIME falso esperado 400, obtenido %d: %s", fakeRec.Code, fakeRec.Body.String())
	}

	// 2) Validar detección de JPEG y WEBP en el detector de cabeceras
	if mime, _, ok := detectAllowedImageMIME(makeValidJPEG(t)); !ok || mime != "image/jpeg" {
		t.Errorf("detectAllowedImageMIME(JPEG) = (%q, %v)", mime, ok)
	}
	if mime, _, ok := detectAllowedImageMIME(makeValidWebPHeader()); !ok || mime != "image/webp" {
		t.Errorf("detectAllowedImageMIME(WEBP) = (%q, %v)", mime, ok)
	}

	// 3) Archivo mayor a 5 MB debe rechazarse con ErrFileTooLarge (413)
	oversized := append(makeValidPNG(t), bytes.Repeat([]byte{0x00}, MaxEvidenciaBytes+10)...)
	_, errBig := svc.UploadComprobante(context.Background(), UploadComprobanteInput{
		ReservaID:       reservaID,
		JugadorID:       jugador1,
		MetodoPagoID:    yapeID,
		CodigoOperacion: "OP-BIG",
		MontoDeclarado:  75.00,
		FileData:        oversized,
	})
	if !errors.Is(errBig, ErrFileTooLarge) {
		t.Fatalf("archivo >5MB esperado ErrFileTooLarge, obtenido: %v", errBig)
	}

	// 4) Subir PNG real -> 200 OK + estado POR_VALIDAR + notificación creada para dueño
	validPNG := makeValidPNG(t)
	okReq := buildMultipartComprobanteRequest(t, reservaID, yapeID, "YAPE-998877", "75.00", "yape.png", validPNG)
	okReq.Header.Set("Authorization", "Bearer j1")
	okRec := httptest.NewRecorder()
	router.ServeHTTP(okRec, okReq)
	if okRec.Code != http.StatusOK {
		t.Fatalf("upload PNG válido esperado 200, obtenido %d: %s", okRec.Code, okRec.Body.String())
	}

	var uploadEnvelope struct {
		Data UploadComprobanteResponse `json:"data"`
	}
	if err := json.Unmarshal(okRec.Body.Bytes(), &uploadEnvelope); err != nil {
		t.Fatalf("error decodificando respuesta upload: %v", err)
	}
	if uploadEnvelope.Data.EstadoReserva != EstadoPorValidar {
		t.Errorf("estado_reserva = %q, esperado POR_VALIDAR", uploadEnvelope.Data.EstadoReserva)
	}
	if store.notifsCount != 1 {
		t.Errorf("notifsCount = %d, esperado 1 notificación para el dueño", store.notifsCount)
	}

	evidenciaURL := "/api/v1/comprobantes/" + uploadEnvelope.Data.Comprobante.ID.String() + "/evidencia"

	// 5) Matriz de control de acceso sobre GET /api/v1/comprobantes/{id}/evidencia
	accessCases := []struct {
		name       string
		token      string
		wantStatus int
	}{
		{name: "sin autenticacion", token: "", wantStatus: http.StatusUnauthorized},
		{name: "jugador propietario", token: "j1", wantStatus: http.StatusOK},
		{name: "dueno del complejo", token: "dueno", wantStatus: http.StatusOK},
		{name: "otro jugador", token: "j2", wantStatus: http.StatusForbidden},
		{name: "otro dueno", token: "otro-dueno", wantStatus: http.StatusForbidden},
		{name: "super admin", token: "admin", wantStatus: http.StatusForbidden},
	}

	for _, tc := range accessCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, evidenciaURL, nil)
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("caso %s: status=%d, esperado=%d (body=%s)", tc.name, rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantStatus == http.StatusOK {
				if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
					t.Errorf("Content-Type = %q, esperado image/png", ct)
				}
				if !bytes.Equal(rec.Body.Bytes(), validPNG) {
					t.Error("los bytes devueltos de la evidencia no coinciden con el PNG subido")
				}
			}
		})
	}
}

// ---------- PRUEBA 8: Detalle de reserva Modo En Ruta y visibilidad estricta de codigo_qr ----------

func TestJugadorReservaDetailQRVisibilityAndRouteInfo(t *testing.T) {
	store, complejo, cancha, _, now := setupBaseFixture(t)
	loc := LimaLocation()
	jugadorID := uuid.New()
	otroJugadorID := uuid.New()

	svc := NewService(store, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.now = func() time.Time { return now }

	resID := uuid.New()
	qr := "QR-FDP-SECRET-123"
	store.reservas[resID] = &fakeReservaRecord{
		ID:             resID,
		ComplejoID:     complejo.ID,
		CanchaID:       cancha.ID,
		JugadorID:      jugadorID,
		Estado:         EstadoPorValidar,
		Inicio:         time.Date(2026, 11, 26, 17, 0, 0, 0, loc),
		Fin:            time.Date(2026, 11, 26, 19, 0, 0, 0, loc),
		PrecioBase:     150,
		MontoTotal:     150,
		MontoAdelanto:  75,
		SaldoPendiente: 75,
		CodigoQR:       &qr,
		CreatedAt:      now,
	}

	// Otro jugador no puede ver el detalle
	if _, err := svc.GetJugadorReservaDetail(context.Background(), otroJugadorID, resID); !errors.Is(err, ErrReservaForbidden) {
		t.Fatalf("otro jugador debía recibir ErrReservaForbidden, obtenido: %v", err)
	}

	// En POR_VALIDAR no debe exponer codigo_qr, pero sí lat/lng/dirección/referencias
	detailPV, err := svc.GetJugadorReservaDetail(context.Background(), jugadorID, resID)
	if err != nil {
		t.Fatalf("GetJugadorReservaDetail falló: %v", err)
	}
	if detailPV.CodigoQR != nil {
		t.Errorf("CodigoQR debía ser nil en estado POR_VALIDAR, obtenido: %v", *detailPV.CodigoQR)
	}
	if detailPV.Complejo.Lat == nil || detailPV.Complejo.Lng == nil || detailPV.Complejo.Referencias == nil {
		t.Error("el detalle debía incluir lat, lng y referencias para Modo En Ruta")
	}

	// En CONFIRMADA SÍ debe devolver codigo_qr
	store.reservas[resID].Estado = EstadoConfirmada
	detailConf, err := svc.GetJugadorReservaDetail(context.Background(), jugadorID, resID)
	if err != nil {
		t.Fatalf("GetJugadorReservaDetail CONFIRMADA falló: %v", err)
	}
	if detailConf.CodigoQR == nil || *detailConf.CodigoQR != qr {
		t.Errorf("CodigoQR debía devolverse en estado CONFIRMADA, obtenido: %v", detailConf.CodigoQR)
	}
}

func buildMultipartComprobanteRequest(
	t *testing.T,
	reservaID, metodoPagoID uuid.UUID,
	codigoOp, monto, fileName string,
	content []byte,
) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	_ = writer.WriteField("metodo_pago_id", metodoPagoID.String())
	_ = writer.WriteField("codigo_operacion", codigoOp)
	_ = writer.WriteField("monto_declarado", monto)

	part, err := writer.CreateFormFile("evidencia", fileName)
	if err != nil {
		t.Fatalf("error creando parte multipart: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("error escribiendo contenido multipart: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("error cerrando writer multipart: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/jugador/reservas/"+reservaID.String()+"/comprobante", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}
