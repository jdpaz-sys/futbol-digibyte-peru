package suscripciones_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	apphttp "github.com/jdpaz-sys/futbol-digibyte-peru/backend/internal/http"
	"github.com/jdpaz-sys/futbol-digibyte-peru/backend/internal/modules/auth"
	"github.com/jdpaz-sys/futbol-digibyte-peru/backend/internal/modules/suscripciones"
)

type fakeRepo struct {
	planes          map[uuid.UUID]suscripciones.Plan
	createPlanErr   error
	createCompErr   error
	lastCreateParam suscripciones.CreateComplejoParams
	metricas        suscripciones.MetricasGlobalesResponse
	metricasHits    int
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		planes: make(map[uuid.UUID]suscripciones.Plan),
	}
}

func (f *fakeRepo) CreatePlan(_ context.Context, p suscripciones.Plan) (suscripciones.Plan, error) {
	if f.createPlanErr != nil {
		return suscripciones.Plan{}, f.createPlanErr
	}
	p.ID = uuid.New()
	p.CreatedAt = time.Now()
	p.UpdatedAt = p.CreatedAt
	f.planes[p.ID] = p
	return p, nil
}

func (f *fakeRepo) ListPlanes(_ context.Context, activo *bool) ([]suscripciones.Plan, error) {
	out := make([]suscripciones.Plan, 0, len(f.planes))
	for _, p := range f.planes {
		if activo == nil || p.Activo == *activo {
			out = append(out, p)
		}
	}
	return out, nil
}

func (f *fakeRepo) GetPlanByID(_ context.Context, id uuid.UUID) (suscripciones.Plan, error) {
	p, ok := f.planes[id]
	if !ok {
		return suscripciones.Plan{}, suscripciones.ErrPlanNotFound
	}
	return p, nil
}

func (f *fakeRepo) UpdatePlan(_ context.Context, p suscripciones.Plan) (suscripciones.Plan, error) {
	if _, ok := f.planes[p.ID]; !ok {
		return suscripciones.Plan{}, suscripciones.ErrPlanNotFound
	}
	p.UpdatedAt = time.Now()
	f.planes[p.ID] = p
	return p, nil
}

func (f *fakeRepo) CreateComplejoWithOwner(_ context.Context, params suscripciones.CreateComplejoParams) (suscripciones.ComplejoDetalleResponse, error) {
	if f.createCompErr != nil {
		return suscripciones.ComplejoDetalleResponse{}, f.createCompErr
	}
	plan, ok := f.planes[params.PlanID]
	if !ok {
		return suscripciones.ComplejoDetalleResponse{}, suscripciones.ErrPlanNotFound
	}
	f.lastCreateParam = params

	now := time.Now()
	inicio, fin, err := suscripciones.CalcularVigencia(now, nil, plan.UnidadDuracion, int(plan.CantidadDuracion))
	if err != nil {
		return suscripciones.ComplejoDetalleResponse{}, err
	}
	estado := suscripciones.EstadoActiva
	if plan.Periodicidad == suscripciones.PeriodicidadPrueba {
		estado = suscripciones.EstadoPrueba
	}

	compID := uuid.New()
	tel := params.DuenoTelefono
	return suscripciones.ComplejoDetalleResponse{
		ComplejoItem: suscripciones.ComplejoItem{
			ID:                 compID,
			Slug:               params.Slug,
			Nombre:             params.Nombre,
			Departamento:       params.Departamento,
			Provincia:          params.Provincia,
			Distrito:           params.Distrito,
			Publicado:          false,
			EstadoSuscripcion:  estado,
			SuscripcionVenceAt: &fin,
			Dueno: suscripciones.DuenoResumen{
				ID:             uuid.New(),
				Email:          params.DuenoEmail,
				NombreCompleto: params.DuenoNombre,
				Telefono:       &tel,
				Activo:         true,
			},
			PlanActual: &suscripciones.PlanVigenteResumen{
				ID:           plan.ID,
				Codigo:       plan.Codigo,
				Nombre:       plan.Nombre,
				Periodicidad: plan.Periodicidad,
				Precio:       plan.Precio,
			},
			CreatedAt: now,
			UpdatedAt: now,
		},
		Historial: []suscripciones.SuscripcionHistorialItem{
			{
				ID:            uuid.New(),
				ComplejoID:    compID,
				PlanID:        plan.ID,
				PlanNombre:    plan.Nombre,
				Periodicidad:  plan.Periodicidad,
				MontoCobrado:  plan.Precio,
				Estado:        estado,
				InicioAt:      inicio,
				FinAt:         fin,
				RegistradoPor: params.AdminID,
				CreatedAt:     now,
			},
		},
	}, nil
}

func (f *fakeRepo) ListComplejos(_ context.Context, _ suscripciones.ListComplejosFilter) ([]suscripciones.ComplejoItem, int, error) {
	return []suscripciones.ComplejoItem{}, 0, nil
}

func (f *fakeRepo) GetComplejoDetail(_ context.Context, _ uuid.UUID) (suscripciones.ComplejoDetalleResponse, error) {
	return suscripciones.ComplejoDetalleResponse{}, suscripciones.ErrComplejoNotFound
}

func (f *fakeRepo) RenovarSuscripcion(_ context.Context, _ suscripciones.RenovarSuscripcionParams) (suscripciones.OperacionSuscripcionResponse, error) {
	return suscripciones.OperacionSuscripcionResponse{}, suscripciones.ErrComplejoNotFound
}

func (f *fakeRepo) CambiarEstadoSuscripcion(_ context.Context, _ suscripciones.CambiarEstadoParams) (suscripciones.OperacionSuscripcionResponse, error) {
	return suscripciones.OperacionSuscripcionResponse{}, suscripciones.ErrComplejoNotFound
}

func (f *fakeRepo) GetMetricasGlobales(_ context.Context) (suscripciones.MetricasGlobalesResponse, error) {
	f.metricasHits++
	return f.metricas, nil
}

type fakeMetricsCache struct {
	stored      *suscripciones.MetricasGlobalesResponse
	invalidated int
}

func (c *fakeMetricsCache) Get(_ context.Context) (*suscripciones.MetricasGlobalesResponse, error) {
	return c.stored, nil
}

func (c *fakeMetricsCache) Set(_ context.Context, data suscripciones.MetricasGlobalesResponse, _ time.Duration) error {
	cp := data
	c.stored = &cp
	return nil
}

func (c *fakeMetricsCache) Invalidate(_ context.Context) error {
	c.stored = nil
	c.invalidated++
	return nil
}

func TestCalcularVigencia(t *testing.T) {
	lima, err := time.LoadLocation("America/Lima")
	if err != nil {
		lima = time.FixedZone("America/Lima", -5*60*60)
	}

	baseNow := time.Date(2026, 11, 25, 10, 30, 0, 0, lima)

	t.Run("plan PRUEBA en DIAS", func(t *testing.T) {
		inicio, fin, err := suscripciones.CalcularVigencia(baseNow, nil, suscripciones.UnidadDias, 15)
		if err != nil {
			t.Fatalf("error inesperado: %v", err)
		}
		expectedFin := time.Date(2026, 12, 10, 10, 30, 0, 0, lima)
		if !inicio.Equal(baseNow) || !fin.Equal(expectedFin) {
			t.Fatalf("vigencia PRUEBA incorrecta: inicio=%v fin=%v (esperado fin=%v)", inicio, fin, expectedFin)
		}
	})

	t.Run("plan MENSUAL estandar y ajuste fin de mes (clamping)", func(t *testing.T) {
		_, finNormal, err := suscripciones.CalcularVigencia(baseNow, nil, suscripciones.UnidadMeses, 1)
		if err != nil {
			t.Fatalf("error inesperado: %v", err)
		}
		if want := time.Date(2026, 12, 25, 10, 30, 0, 0, lima); !finNormal.Equal(want) {
			t.Fatalf("fin mensual incorrecto: got %v, want %v", finNormal, want)
		}

		jan31 := time.Date(2026, 1, 31, 18, 0, 0, 0, lima)
		_, finClamped, err := suscripciones.CalcularVigencia(jan31, nil, suscripciones.UnidadMeses, 1)
		if err != nil {
			t.Fatalf("error inesperado en clamping: %v", err)
		}
		if want := time.Date(2026, 2, 28, 18, 0, 0, 0, lima); !finClamped.Equal(want) {
			t.Fatalf("clamping febrero no bisiesto incorrecto: got %v, want %v", finClamped, want)
		}

		jan31Leap := time.Date(2028, 1, 31, 18, 0, 0, 0, lima)
		_, finLeap, err := suscripciones.CalcularVigencia(jan31Leap, nil, suscripciones.UnidadMeses, 1)
		if err != nil {
			t.Fatalf("error inesperado en bisiesto: %v", err)
		}
		if want := time.Date(2028, 2, 29, 18, 0, 0, 0, lima); !finLeap.Equal(want) {
			t.Fatalf("clamping febrero bisiesto incorrecto: got %v, want %v", finLeap, want)
		}
	})

	t.Run("plan ANUAL 12 meses", func(t *testing.T) {
		_, fin, err := suscripciones.CalcularVigencia(baseNow, nil, suscripciones.UnidadMeses, 12)
		if err != nil {
			t.Fatalf("error inesperado: %v", err)
		}
		if want := time.Date(2027, 11, 25, 10, 30, 0, 0, lima); !fin.Equal(want) {
			t.Fatalf("fin anual incorrecto: got %v, want %v", fin, want)
		}
	})

	t.Run("renovacion extiende desde suscripcion_vence_at si sigue vigente", func(t *testing.T) {
		venceVigente := time.Date(2026, 12, 10, 15, 0, 0, 0, lima)
		inicio, fin, err := suscripciones.CalcularVigencia(baseNow, &venceVigente, suscripciones.UnidadMeses, 12)
		if err != nil {
			t.Fatalf("error inesperado: %v", err)
		}
		if !inicio.Equal(venceVigente) {
			t.Fatalf("debió iniciar en venceVigente (%v), obtuvo %v", venceVigente, inicio)
		}
		if want := time.Date(2027, 12, 10, 15, 0, 0, 0, lima); !fin.Equal(want) {
			t.Fatalf("fin extendido incorrecto: got %v, want %v", fin, want)
		}
	})

	t.Run("renovacion inicia desde NOW si ya vencio", func(t *testing.T) {
		vencePasado := time.Date(2026, 11, 1, 12, 0, 0, 0, lima)
		inicio, fin, err := suscripciones.CalcularVigencia(baseNow, &vencePasado, suscripciones.UnidadMeses, 1)
		if err != nil {
			t.Fatalf("error inesperado: %v", err)
		}
		if !inicio.Equal(baseNow) {
			t.Fatalf("debió reiniciar desde baseNow (%v), obtuvo %v", baseNow, inicio)
		}
		if want := time.Date(2026, 12, 25, 10, 30, 0, 0, lima); !fin.Equal(want) {
			t.Fatalf("fin desde NOW incorrecto: got %v, want %v", fin, want)
		}
	})
}

func TestNormalizeHelpers(t *testing.T) {
	if got := suscripciones.NormalizeSlug("  Complejo El Ñato - Huacho!  "); got != "complejo-el-nato-huacho" {
		t.Fatalf("NormalizeSlug inesperado: %q", got)
	}
	if got := suscripciones.NormalizePlanCode("Plan Anual Élite 2026"); got != "PLAN_ANUAL_ELITE_2026" {
		t.Fatalf("NormalizePlanCode inesperado: %q", got)
	}
}

func TestPlanAndComplejoValidations(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	cache := &fakeMetricsCache{}
	svc := suscripciones.NewService(repo, cache)

	// 1. Plan PRUEBA con precio > 0 debe fallar con ValidationError
	_, err := svc.CreatePlan(ctx, suscripciones.CreatePlanRequest{
		Nombre:           "Prueba Incorrecta",
		Periodicidad:     "PRUEBA",
		UnidadDuracion:   "DIAS",
		CantidadDuracion: 7,
		Precio:           19.90,
	})
	var valErr *suscripciones.ValidationError
	if !errors.As(err, &valErr) || valErr.Fields["precio"] == "" {
		t.Fatalf("esperaba ValidationError en precio para PRUEBA, obtuvo: %v", err)
	}

	// 2. Crear plan MENSUAL válido y verificar edición segura
	planMensual, err := svc.CreatePlan(ctx, suscripciones.CreatePlanRequest{
		Nombre:           "Plan Mensual Pro",
		Periodicidad:     "MENSUAL",
		UnidadDuracion:   "MESES",
		CantidadDuracion: 1,
		Precio:           99.90,
	})
	if err != nil {
		t.Fatalf("error creando plan mensual: %v", err)
	}
	if planMensual.Codigo != "PLAN_MENSUAL_PRO" || !planMensual.Activo {
		t.Fatalf("plan creado con datos inesperados: %+v", planMensual)
	}

	nuevoPrecio := 119.90
	actualizado, err := svc.UpdatePlan(ctx, planMensual.ID, suscripciones.UpdatePlanRequest{
		Precio: &nuevoPrecio,
	})
	if err != nil || actualizado.Precio != 119.90 {
		t.Fatalf("error actualizando precio del plan: err=%v plan=%+v", err, actualizado)
	}

	// 3. Alta de complejo + DUENO con Argon2id real, defaults geográficos e invalidación de caché
	_ = cache.Set(ctx, suscripciones.MetricasGlobalesResponse{}, time.Minute)
	adminID := uuid.New()
	comp, err := svc.CreateComplejo(ctx, adminID, suscripciones.CreateComplejoRequest{
		Nombre: "La Bombonera Huacho",
		PlanID: planMensual.ID.String(),
		Dueno: suscripciones.CreateDuenoInput{
			Email:          "  DUENO@Bombonera.PE ",
			Password:       "ClaveSegura2026!",
			NombreCompleto: "Carlos Mendoza",
			Telefono:       "+51 987654321",
		},
	})
	if err != nil {
		t.Fatalf("error en CreateComplejo: %v", err)
	}
	if comp.Slug != "la-bombonera-huacho" || comp.Publicado || comp.Departamento != "Lima" || comp.Distrito != "Huacho" {
		t.Fatalf("defaults de complejo incorrectos: %+v", comp)
	}
	if comp.Dueno.Email != "dueno@bombonera.pe" {
		t.Fatalf("email no normalizado: %q", comp.Dueno.Email)
	}
	okHash, err := auth.VerifyPassword("ClaveSegura2026!", repo.lastCreateParam.DuenoPassHash)
	if err != nil || !okHash {
		t.Fatalf("hash Argon2id generado para el dueño no verifica: ok=%v err=%v", okHash, err)
	}
	if cache.invalidated != 1 {
		t.Fatalf("esperaba invalidación de caché de métricas tras crear complejo, obtuvo %d", cache.invalidated)
	}

	// 4. Cambiar estado sin motivo debe ser rechazado
	_, err = svc.CambiarEstadoSuscripcion(ctx, adminID, comp.ID, suscripciones.CambiarEstadoSuscripcionRequest{
		Estado: "SUSPENDIDA",
	})
	if !errors.As(err, &valErr) || valErr.Fields["motivo"] == "" {
		t.Fatalf("esperaba ValidationError por falta de motivo al suspender, obtuvo: %v", err)
	}
}

func TestAdminRoutesRBACAndHTTPErrors(t *testing.T) {
	repo := newFakeRepo()
	repo.metricas.Complejos.Activa = 3
	cache := &fakeMetricsCache{}
	svc := suscripciones.NewService(repo, cache)
	silentLogger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := suscripciones.NewHandler(svc, silentLogger)

	adminID := uuid.New()
	duenoID := uuid.New()
	jugadorID := uuid.New()

	parseToken := func(token string) (apphttp.AuthUser, error) {
		switch token {
		case "tok-super-admin":
			return apphttp.AuthUser{UserID: adminID, Rol: apphttp.RoleSuperAdmin}, nil
		case "tok-dueno":
			return apphttp.AuthUser{UserID: duenoID, Rol: apphttp.RoleDueno}, nil
		case "tok-jugador":
			return apphttp.AuthUser{UserID: jugadorID, Rol: apphttp.RoleJugador}, nil
		default:
			return apphttp.AuthUser{}, errors.New("token inválido")
		}
	}

	router := apphttp.NewRouter(apphttp.RouterConfig{
		Logger:      silentLogger,
		TokenParser: parseToken,
		MountAdmin: func(admin chi.Router) {
			handler.RegisterAdminRoutes(admin)
		},
	})

	// 1. Sin token -> 401
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/metricas", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("sin token esperaba 401, obtuvo %d", rec.Code)
	}

	// 2. DUENO y JUGADOR rechazados con 403 Forbidden en rutas /api/v1/admin/*
	for _, tc := range []struct {
		name   string
		token  string
		method string
		path   string
	}{
		{"dueno en metricas", "tok-dueno", http.MethodGet, "/api/v1/admin/metricas"},
		{"dueno en planes", "tok-dueno", http.MethodGet, "/api/v1/admin/planes"},
		{"dueno creando complejo", "tok-dueno", http.MethodPost, "/api/v1/admin/complejos"},
		{"jugador en metricas", "tok-jugador", http.MethodGet, "/api/v1/admin/metricas"},
		{"jugador creando plan", "tok-jugador", http.MethodPost, "/api/v1/admin/planes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.path, nil)
			r.Header.Set("Authorization", "Bearer "+tc.token)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			if w.Code != http.StatusForbidden {
				t.Fatalf("%s: esperaba 403 Forbidden, obtuvo %d (%s)", tc.name, w.Code, w.Body.String())
			}
		})
	}

	// 3. SUPER_ADMIN accede a metricas (200 OK) y segundo llamado golpea caché Valkey
	for i := 0; i < 2; i++ {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/admin/metricas", nil)
		r.Header.Set("Authorization", "Bearer tok-super-admin")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("SUPER_ADMIN en metricas esperaba 200, obtuvo %d", w.Code)
		}
	}
	if repo.metricasHits != 1 {
		t.Fatalf("esperaba 1 consulta a repositorio gracias al caché, obtuvo %d", repo.metricasHits)
	}

	// 4. UUID inválido -> 400, recurso inexistente -> 404, campo desconocido -> 400, conflicto email -> 409
	rBadUUID := httptest.NewRequest(http.MethodGet, "/api/v1/admin/complejos/no-es-uuid", nil)
	rBadUUID.Header.Set("Authorization", "Bearer tok-super-admin")
	wBadUUID := httptest.NewRecorder()
	router.ServeHTTP(wBadUUID, rBadUUID)
	if wBadUUID.Code != http.StatusBadRequest {
		t.Fatalf("UUID inválido esperaba 400, obtuvo %d", wBadUUID.Code)
	}

	rNotFound := httptest.NewRequest(http.MethodGet, "/api/v1/admin/complejos/"+uuid.NewString(), nil)
	rNotFound.Header.Set("Authorization", "Bearer tok-super-admin")
	wNotFound := httptest.NewRecorder()
	router.ServeHTTP(wNotFound, rNotFound)
	if wNotFound.Code != http.StatusNotFound {
		t.Fatalf("complejo inexistente esperaba 404, obtuvo %d", wNotFound.Code)
	}

	rUnknownField := httptest.NewRequest(http.MethodPost, "/api/v1/admin/planes", bytes.NewBufferString(`{"nombre":"Test","campo_extra":true}`))
	rUnknownField.Header.Set("Authorization", "Bearer tok-super-admin")
	wUnknownField := httptest.NewRecorder()
	router.ServeHTTP(wUnknownField, rUnknownField)
	if wUnknownField.Code != http.StatusBadRequest {
		t.Fatalf("campo JSON desconocido esperaba 400, obtuvo %d", wUnknownField.Code)
	}

	planBody, _ := json.Marshal(suscripciones.CreatePlanRequest{
		Nombre:           "Plan Anual Test",
		Periodicidad:     "ANUAL",
		UnidadDuracion:   "MESES",
		CantidadDuracion: 12,
		Precio:           990.00,
	})
	rPlan := httptest.NewRequest(http.MethodPost, "/api/v1/admin/planes", bytes.NewReader(planBody))
	rPlan.Header.Set("Authorization", "Bearer tok-super-admin")
	wPlan := httptest.NewRecorder()
	router.ServeHTTP(wPlan, rPlan)
	if wPlan.Code != http.StatusCreated {
		t.Fatalf("crear plan esperaba 201, obtuvo %d (%s)", wPlan.Code, wPlan.Body.String())
	}

	repo.createPlanErr = suscripciones.ErrPlanCodeAlreadyExists
	rDupPlan := httptest.NewRequest(http.MethodPost, "/api/v1/admin/planes", bytes.NewReader(planBody))
	rDupPlan.Header.Set("Authorization", "Bearer tok-super-admin")
	wDupPlan := httptest.NewRecorder()
	router.ServeHTTP(wDupPlan, rDupPlan)
	if wDupPlan.Code != http.StatusConflict {
		t.Fatalf("código duplicado esperaba 409 Conflict, obtuvo %d", wDupPlan.Code)
	}
}
