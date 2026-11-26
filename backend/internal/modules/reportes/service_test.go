package reportes

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	apphttp "github.com/jdpaz-sys/futbol-digibyte-peru/backend/internal/http"
)

type fakeReportesStore struct {
	gotComplejoID uuid.UUID
	gotStart      time.Time
	gotEnd        time.Time
	agg           PeriodoAggregates
	err           error
}

func (f *fakeReportesStore) GetPeriodoAggregates(_ context.Context, complejoID uuid.UUID, rangeStart, rangeEnd time.Time) (PeriodoAggregates, error) {
	f.gotComplejoID = complejoID
	f.gotStart = rangeStart
	f.gotEnd = rangeEnd
	if f.err != nil {
		return PeriodoAggregates{}, f.err
	}
	return f.agg, nil
}

func TestGetResumenDefault30DaysAmericaLima(t *testing.T) {
	// 2026-11-26 02:30 UTC == 2026-11-25 21:30 America/Lima -> "hoy" en Lima debe ser 2026-11-25.
	fixedNow := time.Date(2026, 11, 26, 2, 30, 0, 0, time.UTC)
	complejoID := uuid.New()
	cancha1 := uuid.New()
	cancha2 := uuid.New()

	store := &fakeReportesStore{
		agg: PeriodoAggregates{
			Finanzas: FinanzasResumen{
				IngresosConfirmadosTotal:  425.5000001,
				AdelantosCobrados:         275.50,
				SaldosCobrados:            150.00,
				SaldosPendientesPorCobrar: 180.25,
				ReembolsosPendientesCount: 2,
			},
			PorEstado: map[string]int{
				EstadoConfirmada: 3,
				EstadoCompletada: 4,
				EstadoNoShow:     1,
				EstadoCancelada:  2,
			},
			PorOrigen: map[string]int{
				OrigenApp:     7,
				OrigenExterna: 2,
				OrigenBloqueo: 1,
			},
			Horarios: []HorarioDia{
				{DiaSemana: 0, MinutoApertura: 480, MinutoCierre: 1200, Cerrado: true},  // Domingo cerrado
				{DiaSemana: 1, MinutoApertura: 480, MinutoCierre: 1320, Cerrado: false}, // 14h
				{DiaSemana: 2, MinutoApertura: 480, MinutoCierre: 1320, Cerrado: false}, // 14h
				{DiaSemana: 3, MinutoApertura: 480, MinutoCierre: 1320, Cerrado: false}, // 14h
				{DiaSemana: 4, MinutoApertura: 480, MinutoCierre: 1320, Cerrado: false}, // 14h
				{DiaSemana: 5, MinutoApertura: 480, MinutoCierre: 1320, Cerrado: false}, // 14h
				{DiaSemana: 6, MinutoApertura: 480, MinutoCierre: 1080, Cerrado: false}, // 10h
			},
			Canchas: []CanchaOcupacionRaw{
				{
					CanchaID:          cancha1,
					CanchaNombre:      "Cancha 1",
					Modalidad:         "7v7",
					Activa:            true,
					ReservasEfectivas: 5,
					MinutosOcupados:   360, // 6h
					MinutosBloqueados: 120, // 2h
				},
				{
					CanchaID:          cancha2,
					CanchaNombre:      "Cancha 2",
					Modalidad:         "6v6",
					Activa:            true,
					ReservasEfectivas: 0,
					MinutosOcupados:   0,
					MinutosBloqueados: 0,
				},
			},
		},
	}

	svc := NewService(store)
	svc.now = func() time.Time { return fixedNow }

	res, err := svc.GetResumen(context.Background(), complejoID, "", "")
	if err != nil {
		t.Fatalf("error inesperado en GetResumen: %v", err)
	}

	if res.Desde != "2026-10-27" || res.Hasta != "2026-11-25" {
		t.Fatalf("rango por defecto incorrecto: got %s..%s, want 2026-10-27..2026-11-25", res.Desde, res.Hasta)
	}
	if store.gotComplejoID != complejoID {
		t.Fatalf("complejoID no propagado al store")
	}
	wantStart := time.Date(2026, 10, 27, 0, 0, 0, 0, limaLocation)
	wantEnd := time.Date(2026, 11, 26, 0, 0, 0, 0, limaLocation)
	if !store.gotStart.Equal(wantStart) || !store.gotEnd.Equal(wantEnd) {
		t.Fatalf("rango semiabierto inesperado: [%v, %v), want [%v, %v)", store.gotStart, store.gotEnd, wantStart, wantEnd)
	}

	if res.Finanzas.IngresosConfirmadosTotal != 425.50 {
		t.Fatalf("ingresos_confirmados_total = %.2f, want 425.50", res.Finanzas.IngresosConfirmadosTotal)
	}
	if res.Finanzas.AdelantosCobrados != 275.50 || res.Finanzas.SaldosCobrados != 150.00 {
		t.Fatalf("adelantos/saldos incorrectos: %+v", res.Finanzas)
	}
	if res.Finanzas.SaldosPendientesPorCobrar != 180.25 || res.Finanzas.ReembolsosPendientesCount != 2 {
		t.Fatalf("saldos pendientes / reembolsos incorrectos: %+v", res.Finanzas)
	}

	// Todos los 8 estados y 3 orígenes deben estar presentes aunque sean 0.
	if len(res.Conteos.PorEstado) != 8 {
		t.Fatalf("se esperaban 8 claves en por_estado, got %d", len(res.Conteos.PorEstado))
	}
	if res.Conteos.Total != 10 || res.Conteos.PorEstado[EstadoPendiente] != 0 || res.Conteos.PorEstado[EstadoCompletada] != 4 {
		t.Fatalf("conteos por estado incorrectos: %+v", res.Conteos)
	}
	if len(res.Conteos.PorOrigen) != 3 || res.Conteos.PorOrigen[OrigenBloqueo] != 1 {
		t.Fatalf("conteos por origen incorrectos: %+v", res.Conteos.PorOrigen)
	}

	if len(res.OcupacionPorCancha) != 2 {
		t.Fatalf("se esperaban 2 canchas en ocupación, got %d", len(res.OcupacionPorCancha))
	}
	// Del 2026-10-27 (mar) al 2026-11-25 (mié) = 30 días: 4 domingos (cerrado), 4 sábados (10h), 22 días L-V (14h) => 40 + 308 = 348h.
	if res.OcupacionPorCancha[0].HorasDisponibles != 348.0 {
		t.Fatalf("horas_disponibles = %.2f, want 348.00", res.OcupacionPorCancha[0].HorasDisponibles)
	}
	if res.OcupacionPorCancha[0].HorasOcupadas != 6.0 || res.OcupacionPorCancha[0].HorasBloqueadas != 2.0 {
		t.Fatalf("horas ocupadas/bloqueadas incorrectas: %+v", res.OcupacionPorCancha[0])
	}
	wantPct := round2((360.0 / (348.0 * 60.0)) * 100.0)
	if res.OcupacionPorCancha[0].TasaOcupacionPorcentaje != wantPct {
		t.Fatalf("tasa_ocupacion_porcentaje = %.2f, want %.2f", res.OcupacionPorCancha[0].TasaOcupacionPorcentaje, wantPct)
	}
}

func TestGetResumenZeroAvailableHoursAndValidations(t *testing.T) {
	store := &fakeReportesStore{
		agg: PeriodoAggregates{
			PorEstado: map[string]int{},
			PorOrigen: map[string]int{},
			Horarios:  []HorarioDia{}, // Sin horarios configurados -> 0h disponibles, sin división por cero
			Canchas: []CanchaOcupacionRaw{
				{
					CanchaID:        uuid.New(),
					CanchaNombre:    "Cancha Unica",
					Modalidad:       "5v5",
					Activa:          true,
					MinutosOcupados: 60,
				},
			},
		},
	}

	svc := NewService(store)
	svc.now = func() time.Time { return time.Date(2026, 11, 25, 12, 0, 0, 0, limaLocation) }

	res, err := svc.GetResumen(context.Background(), uuid.New(), "2026-11-25", "2026-11-25")
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if res.OcupacionPorCancha[0].HorasDisponibles != 0 || res.OcupacionPorCancha[0].TasaOcupacionPorcentaje != 0 {
		t.Fatalf("esperaba 0h y 0%% sin horarios: %+v", res.OcupacionPorCancha[0])
	}

	// Fecha inválida
	if _, err := svc.GetResumen(context.Background(), uuid.New(), "2026-13-01", "2026-11-25"); err == nil {
		t.Fatalf("esperaba error por fecha desde inválida")
	}

	// desde > hasta
	if _, err := svc.GetResumen(context.Background(), uuid.New(), "2026-11-26", "2026-11-25"); err == nil {
		t.Fatalf("esperaba error cuando desde > hasta")
	}

	// Rango > 366 días
	if _, err := svc.GetResumen(context.Background(), uuid.New(), "2025-01-01", "2026-11-25"); err == nil {
		t.Fatalf("esperaba error cuando el rango excede 366 días")
	}
}

func TestHandlerResumenTenantIsolationAndValidation(t *testing.T) {
	dueno1 := uuid.New()
	dueno2 := uuid.New()
	complejo1 := uuid.New()
	complejo2 := uuid.New()

	store := &fakeReportesStore{
		agg: PeriodoAggregates{
			Finanzas:  FinanzasResumen{IngresosConfirmadosTotal: 120.0},
			PorEstado: map[string]int{EstadoCompletada: 1},
			PorOrigen: map[string]int{OrigenApp: 1},
			Horarios: []HorarioDia{
				{DiaSemana: 3, MinutoApertura: 600, MinutoCierre: 1200, Cerrado: false}, // 10h
			},
			Canchas: []CanchaOcupacionRaw{
				{CanchaID: uuid.New(), CanchaNombre: "Principal", Modalidad: "7v7", Activa: true, ReservasEfectivas: 1, MinutosOcupados: 120},
			},
		},
	}

	svc := NewService(store)
	svc.now = func() time.Time { return time.Date(2026, 11, 25, 12, 0, 0, 0, limaLocation) }
	h := NewHandler(svc, nil)

	router := apphttp.NewRouter(apphttp.RouterConfig{
		TokenParser: func(token string) (apphttp.AuthUser, error) {
			switch token {
			case "dueno-1":
				return apphttp.AuthUser{UserID: dueno1, Rol: apphttp.RoleDueno, ComplejoID: &complejo1}, nil
			case "dueno-2":
				return apphttp.AuthUser{UserID: dueno2, Rol: apphttp.RoleDueno, ComplejoID: &complejo2}, nil
			default:
				return apphttp.AuthUser{}, errors.New("token inválido")
			}
		},
		TenantChecker: func(_ context.Context, userID, complejoID uuid.UUID) (bool, error) {
			return (userID == dueno1 && complejoID == complejo1) || (userID == dueno2 && complejoID == complejo2), nil
		},
		MountDueno: func(dueno chi.Router) {
			h.RegisterDuenoRoutes(dueno)
		},
	})

	// 1. Dueño 1 consulta su propio resumen -> 200 OK
	req := httptest.NewRequest(http.MethodGet, "/api/v1/dueno/reportes/resumen?desde=2026-11-25&hasta=2026-11-25", nil)
	req.Header.Set("Authorization", "Bearer dueno-1")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("esperaba 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var env struct {
		Data ResumenResponse `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("JSON inválido: %v", err)
	}
	if env.Data.ComplejoID != complejo1 || env.Data.OcupacionPorCancha[0].TasaOcupacionPorcentaje != 20.0 {
		t.Fatalf("respuesta inesperada: %+v", env.Data)
	}

	// 2. Dueño 2 intenta acceder al complejo del Dueño 1 vía X-Complejo-ID -> 403 TENANT_FORBIDDEN
	reqCross := httptest.NewRequest(http.MethodGet, "/api/v1/dueno/reportes/resumen?desde=2026-11-25&hasta=2026-11-25", nil)
	reqCross.Header.Set("Authorization", "Bearer dueno-2")
	reqCross.Header.Set("X-Complejo-ID", complejo1.String())
	rrCross := httptest.NewRecorder()
	router.ServeHTTP(rrCross, reqCross)

	if rrCross.Code != http.StatusForbidden {
		t.Fatalf("esperaba 403 en intento cross-tenant, got %d: %s", rrCross.Code, rrCross.Body.String())
	}

	// 3. Fecha inválida -> 400 VALIDATION_ERROR
	reqBad := httptest.NewRequest(http.MethodGet, "/api/v1/dueno/reportes/resumen?desde=invalida", nil)
	reqBad.Header.Set("Authorization", "Bearer dueno-1")
	rrBad := httptest.NewRecorder()
	router.ServeHTTP(rrBad, reqBad)

	if rrBad.Code != http.StatusBadRequest {
		t.Fatalf("esperaba 400 para fecha inválida, got %d: %s", rrBad.Code, rrBad.Body.String())
	}
}
