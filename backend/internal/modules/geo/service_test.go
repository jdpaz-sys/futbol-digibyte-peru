package geo_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/redis/go-redis/v9/maintnotifications"

	apphttp "github.com/jdpaz-sys/futbol-digibyte-peru/backend/internal/http"
	"github.com/jdpaz-sys/futbol-digibyte-peru/backend/internal/modules/geo"
)

// miniValkeyServer simula un servidor Valkey/Redis RESP2 en memoria local (127.0.0.1:0)
// para probar ValkeyCache real (GET/SET/PX) sin depender de red externa ni contenedores en go test.
type miniValkeyEntry struct {
	val   string
	ttlMs int64
}

type miniValkeyServer struct {
	ln      net.Listener
	mu      sync.RWMutex
	entries map[string]miniValkeyEntry
	sets    atomic.Int64
	gets    atomic.Int64
}

func startMiniValkeyServer(t *testing.T) (*miniValkeyServer, *redis.Client) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("error iniciando miniValkeyServer: %v", err)
	}

	srv := &miniValkeyServer{
		ln:      ln,
		entries: make(map[string]miniValkeyEntry),
	}

	go srv.acceptLoop()

	client := redis.NewClient(&redis.Options{
		Addr:            ln.Addr().String(),
		Protocol:        2,
		DisableIdentity: true,
		MaintNotificationsConfig: &maintnotifications.Config{
			Mode: maintnotifications.ModeDisabled,
		},
	})

	t.Cleanup(func() {
		_ = client.Close()
		_ = ln.Close()
	})

	return srv, client
}

func (s *miniValkeyServer) acceptLoop() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handleConn(conn)
	}
}

func (s *miniValkeyServer) handleConn(conn net.Conn) {
	defer func() {
		_ = conn.Close()
	}()

	reader := bufio.NewReader(conn)
	for {
		args, err := readRESPArray(reader)
		if err != nil {
			return
		}
		if len(args) == 0 {
			continue
		}

		cmd := strings.ToUpper(args[0])
		switch cmd {
		case "HELLO":
			_, _ = io.WriteString(conn, "-ERR unknown command 'HELLO'\r\n")
		case "PING":
			_, _ = io.WriteString(conn, "+PONG\r\n")
		case "GET":
			s.gets.Add(1)
			if len(args) < 2 {
				_, _ = io.WriteString(conn, "-ERR wrong number of arguments\r\n")
				continue
			}
			s.mu.RLock()
			entry, ok := s.entries[args[1]]
			s.mu.RUnlock()
			if !ok {
				_, _ = io.WriteString(conn, "$-1\r\n")
				continue
			}
			_, _ = fmt.Fprintf(conn, "$%d\r\n%s\r\n", len(entry.val), entry.val)
		case "SET":
			s.sets.Add(1)
			if len(args) < 3 {
				_, _ = io.WriteString(conn, "-ERR wrong number of arguments\r\n")
				continue
			}
			var ttlMs int64
			for i := 3; i+1 < len(args); i += 2 {
				switch strings.ToUpper(args[i]) {
				case "PX":
					ttlMs, _ = strconv.ParseInt(args[i+1], 10, 64)
				case "EX":
					sec, _ := strconv.ParseInt(args[i+1], 10, 64)
					ttlMs = sec * 1000
				}
			}
			s.mu.Lock()
			s.entries[args[1]] = miniValkeyEntry{val: args[2], ttlMs: ttlMs}
			s.mu.Unlock()
			_, _ = io.WriteString(conn, "+OK\r\n")
		case "DEL":
			var removed int
			s.mu.Lock()
			for _, key := range args[1:] {
				if _, exists := s.entries[key]; exists {
					delete(s.entries, key)
					removed++
				}
			}
			s.mu.Unlock()
			_, _ = fmt.Fprintf(conn, ":%d\r\n", removed)
		default:
			_, _ = io.WriteString(conn, "+OK\r\n")
		}
	}
}

func (s *miniValkeyServer) getEntry(key string) (miniValkeyEntry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.entries[key]
	return e, ok
}

func readRESPArray(r *bufio.Reader) ([]string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	line = strings.TrimRight(line, "\r\n")
	if len(line) == 0 || line[0] != '*' {
		return nil, fmt.Errorf("RESP inesperado: %q", line)
	}
	count, err := strconv.Atoi(line[1:])
	if err != nil {
		return nil, err
	}
	args := make([]string, 0, count)
	for i := 0; i < count; i++ {
		lenLine, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		lenLine = strings.TrimRight(lenLine, "\r\n")
		if len(lenLine) == 0 || lenLine[0] != '$' {
			return nil, fmt.Errorf("bulk string inválido: %q", lenLine)
		}
		n, err := strconv.Atoi(lenLine[1:])
		if err != nil {
			return nil, err
		}
		buf := make([]byte, n+2)
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, err
		}
		args = append(args, string(buf[:n]))
	}
	return args, nil
}

// fakeGeoStore simula PostgreSQL/PostGIS para pruebas unitarias determinísticas.
type fakeGeoStore struct {
	mu               sync.Mutex
	lastExploreQuery geo.ExploreComplejosQuery
	exploreItems     []geo.PublicComplejoItem
	exploreTotal     int
	bySlug           map[string]geo.ComplejoBaseRecord
	slugHits         int
	catalogHits      int
	fotos            []geo.PublicFoto
	canchas          []geo.PublicCancha
	horarios         []geo.PublicHorarioAtencion
	tarifas          []geo.PublicTarifaFranja
	tiposPago        []string
}

func newFakeGeoStore() *fakeGeoStore {
	return &fakeGeoStore{
		bySlug: make(map[string]geo.ComplejoBaseRecord),
	}
}

func (f *fakeGeoStore) ExplorePublicComplejos(_ context.Context, q geo.ExploreComplejosQuery) ([]geo.PublicComplejoItem, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastExploreQuery = q

	out := make([]geo.PublicComplejoItem, len(f.exploreItems))
	copy(out, f.exploreItems)
	if !q.UseGPS {
		for i := range out {
			out[i].DistanciaMetros = nil
		}
	}
	return out, f.exploreTotal, nil
}

func (f *fakeGeoStore) GetComplejoBaseBySlug(_ context.Context, slug string) (geo.ComplejoBaseRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.slugHits++

	rec, ok := f.bySlug[slug]
	if !ok || !rec.Publicado {
		return geo.ComplejoBaseRecord{}, geo.ErrComplejoNotFound
	}
	return rec, nil
}

func (f *fakeGeoStore) GetComplejoPublicCatalog(_ context.Context, _ uuid.UUID) (
	[]geo.PublicFoto,
	[]geo.PublicCancha,
	[]geo.PublicHorarioAtencion,
	[]geo.PublicTarifaFranja,
	[]string,
	error,
) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.catalogHits++
	return f.fotos, f.canchas, f.horarios, f.tarifas, f.tiposPago, nil
}

type fixedRateLimiter struct {
	mu    sync.Mutex
	count map[string]int
}

func newFixedRateLimiter() *fixedRateLimiter {
	return &fixedRateLimiter{count: make(map[string]int)}
}

func (l *fixedRateLimiter) Allow(_ context.Context, key string, limit int, window time.Duration) (bool, int, time.Duration, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.count[key]++
	cur := l.count[key]
	rem := limit - cur
	if rem < 0 {
		rem = 0
	}
	return cur <= limit, rem, window, nil
}

func TestReverseGeocode_ValkeyCacheMissAndHit_Rounding5Decimals(t *testing.T) {
	var externalHits atomic.Int32
	var capturedUA, capturedLat, capturedLon string

	geocoderSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		externalHits.Add(1)
		capturedUA = r.Header.Get("User-Agent")
		capturedLat = r.URL.Query().Get("lat")
		capturedLon = r.URL.Query().Get("lon")

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"display_name": "Jirón La Merced, Huacho, Huaura, Lima, 15136, Perú",
			"address": map[string]string{
				"road":   "Jirón La Merced",
				"city":   "Distrito de Huacho",
				"region": "Provincia de Huaura",
				"state":  "Departamento de Lima",
			},
		})
	}))
	defer geocoderSrv.Close()

	valkeySrv, redisClient := startMiniValkeyServer(t)
	cache := geo.NewValkeyCache(redisClient)
	store := newFakeGeoStore()

	svc := geo.NewService(store, cache, geo.ServiceConfig{
		GeocoderBaseURL:   geocoderSrv.URL,
		GeocoderUserAgent: "FutbolDigibytePeru/1.0",
		Logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	ctx := context.Background()

	// 1. Primera llamada -> Cache MISS (redondea -11.106701 y -77.608804 a 5 decimales)
	res1, err := svc.ReverseGeocode(ctx, -11.106701, -77.608804)
	if err != nil {
		t.Fatalf("ReverseGeocode #1 falló: %v", err)
	}
	if res1.OrigenCache {
		t.Fatalf("esperaba origen_cache=false en primera consulta, obtenido true")
	}
	if externalHits.Load() != 1 {
		t.Fatalf("externalHits = %d, esperado 1", externalHits.Load())
	}
	if capturedUA != "FutbolDigibytePeru/1.0" {
		t.Errorf("User-Agent enviado = %q, esperado FutbolDigibytePeru/1.0", capturedUA)
	}
	if capturedLat != "-11.10670" || capturedLon != "-77.60880" {
		t.Errorf("coordenadas enviadas al geocoder = (%s, %s), esperado (-11.10670, -77.60880)", capturedLat, capturedLon)
	}
	if res1.Departamento != "Lima" || res1.Provincia != "Huaura" || res1.Distrito != "Huacho" {
		t.Errorf("demarcación política inesperada: %+v", res1)
	}
	if res1.DireccionEscrita != "Jirón La Merced, Huacho, Huaura, Lima, 15136, Perú" {
		t.Errorf("direccion_escrita inesperada: %q", res1.DireccionEscrita)
	}

	entry, ok := valkeySrv.getEntry("geo:reverse:-11.10670:-77.60880")
	if !ok {
		t.Fatal("esperaba clave geo:reverse:-11.10670:-77.60880 almacenada en Valkey")
	}
	if entry.ttlMs != int64((24 * time.Hour).Milliseconds()) {
		t.Errorf("TTL en Valkey = %d ms, esperado %d ms (24h)", entry.ttlMs, (24 * time.Hour).Milliseconds())
	}

	// 2. Segunda llamada con variaciones en el 6to decimal -> redondea igual -> Cache HIT
	res2, err := svc.ReverseGeocode(ctx, -11.106704, -77.608799)
	if err != nil {
		t.Fatalf("ReverseGeocode #2 falló: %v", err)
	}
	if !res2.OrigenCache {
		t.Fatalf("esperaba origen_cache=true en segunda consulta, obtenido false")
	}
	if externalHits.Load() != 1 {
		t.Fatalf("el geocoder externo no debía invocarse en cache hit; externalHits = %d", externalHits.Load())
	}
	if res2.DireccionEscrita != res1.DireccionEscrita {
		t.Errorf("direccion_escrita en cache hit = %q, esperado %q", res2.DireccionEscrita, res1.DireccionEscrita)
	}
}

func TestReverseGeocode_ValidationTimeoutErrors_AndDuenoMiddlewares(t *testing.T) {
	duenoUserID := uuid.New()
	complejoID := uuid.New()
	jugadorUserID := uuid.New()

	mode := atomic.Int32{} // 0=ok, 1=500, 2=timeout
	geocoderSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		switch mode.Load() {
		case 1:
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"error":"upstream down"}`)
		case 2:
			time.Sleep(150 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
		default:
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"display_name":"Malecón Roca, Huacho","address":{"road":"Malecón Roca","city":"Huacho","county":"Huaura","state":"Lima"}}`)
		}
	}))
	defer geocoderSrv.Close()

	_, redisClient := startMiniValkeyServer(t)
	svc := geo.NewService(newFakeGeoStore(), geo.NewValkeyCache(redisClient), geo.ServiceConfig{
		GeocoderBaseURL: geocoderSrv.URL,
		HTTPClient:      &http.Client{Timeout: 50 * time.Millisecond},
		Logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	limiter := newFixedRateLimiter()
	handler := geo.NewHandler(svc, limiter, slog.New(slog.NewTextHandler(io.Discard, nil)))

	router := apphttp.NewRouter(apphttp.RouterConfig{
		CORSAllowedOrigins: []string{"http://localhost:3000"},
		Logger:             slog.New(slog.NewTextHandler(io.Discard, nil)),
		TokenParser: func(token string) (apphttp.AuthUser, error) {
			switch token {
			case "dueno-token":
				cid := complejoID
				return apphttp.AuthUser{UserID: duenoUserID, Rol: apphttp.RoleDueno, ComplejoID: &cid}, nil
			case "jugador-token":
				return apphttp.AuthUser{UserID: jugadorUserID, Rol: apphttp.RoleJugador}, nil
			default:
				return apphttp.AuthUser{}, fmt.Errorf("token inválido")
			}
		},
		TenantChecker: func(_ context.Context, uid, cid uuid.UUID) (bool, error) {
			return uid == duenoUserID && cid == complejoID, nil
		},
		MountDueno: func(dueno chi.Router) {
			handler.RegisterDuenoRoutes(dueno)
		},
	})

	// Sin autenticación -> 401
	reqUnauth := httptest.NewRequest(http.MethodGet, "/api/v1/dueno/geo/reverse?lat=-11.1067&lng=-77.6088", nil)
	recUnauth := httptest.NewRecorder()
	router.ServeHTTP(recUnauth, reqUnauth)
	if recUnauth.Code != http.StatusUnauthorized {
		t.Fatalf("sin token status = %d, esperado 401", recUnauth.Code)
	}

	// Rol JUGADOR -> 403
	reqJugador := httptest.NewRequest(http.MethodGet, "/api/v1/dueno/geo/reverse?lat=-11.1067&lng=-77.6088", nil)
	reqJugador.Header.Set("Authorization", "Bearer jugador-token")
	recJugador := httptest.NewRecorder()
	router.ServeHTTP(recJugador, reqJugador)
	if recJugador.Code != http.StatusForbidden {
		t.Fatalf("rol JUGADOR status = %d, esperado 403", recJugador.Code)
	}

	// Validación de rangos de coordenadas -> 400
	invalidURLs := []string{
		"/api/v1/dueno/geo/reverse?lat=-91&lng=-77.6088",
		"/api/v1/dueno/geo/reverse?lat=90.001&lng=-77.6088",
		"/api/v1/dueno/geo/reverse?lat=-11.1067&lng=-180.5",
		"/api/v1/dueno/geo/reverse?lat=-11.1067&lng=181",
		"/api/v1/dueno/geo/reverse?lat=-11.1067",
	}
	for _, u := range invalidURLs {
		req := httptest.NewRequest(http.MethodGet, u, nil)
		req.Header.Set("Authorization", "Bearer dueno-token")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("URL inválida %s devolvió status %d, esperado 400", u, rec.Code)
		}
	}

	// Proveedor externo responde 500 -> 502 Bad Gateway
	mode.Store(1)
	req502 := httptest.NewRequest(http.MethodGet, "/api/v1/dueno/geo/reverse?lat=-11.1100&lng=-77.6100", nil)
	req502.Header.Set("Authorization", "Bearer dueno-token")
	rec502 := httptest.NewRecorder()
	router.ServeHTTP(rec502, req502)
	if rec502.Code != http.StatusBadGateway {
		t.Fatalf("fallo upstream status = %d, esperado 502; body=%s", rec502.Code, rec502.Body.String())
	}

	// Proveedor externo excede timeout -> 504 Gateway Timeout
	mode.Store(2)
	req504 := httptest.NewRequest(http.MethodGet, "/api/v1/dueno/geo/reverse?lat=-11.1200&lng=-77.6200", nil)
	req504.Header.Set("Authorization", "Bearer dueno-token")
	rec504 := httptest.NewRecorder()
	router.ServeHTTP(rec504, req504)
	if rec504.Code != http.StatusGatewayTimeout {
		t.Fatalf("timeout upstream status = %d, esperado 504; body=%s", rec504.Code, rec504.Body.String())
	}
}

func TestExploreComplejos_PostGISAndDegradationWithoutGPS_StrictPrivacy(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))

	valkeySrv, redisClient := startMiniValkeyServer(t)
	store := newFakeGeoStore()
	dist := 342.55
	precio := 60.0
	portada := "https://cdn.digibyte.pe/complejos/portada.jpg"
	ref := "Frente al óvalo de Huacho"

	store.exploreItems = []geo.PublicComplejoItem{
		{
			ID:               uuid.New(),
			Slug:             "complejo-el-golazo-huacho",
			Nombre:           "Complejo El Golazo Huacho",
			DireccionEscrita: "Av. Tupac Amaru 210",
			Referencias:      &ref,
			Distrito:         "Huacho",
			Provincia:        "Huaura",
			Departamento:     "Lima",
			Lat:              -11.1080,
			Lng:              -77.6050,
			DistanciaMetros:  &dist,
			FotoPortadaURL:   &portada,
			TarifaDesde:      &precio,
			Modalidades:      []string{"6v6", "7v7"},
			Servicios:        []string{"estacionamiento", "chalecos"},
		},
	}
	store.exploreTotal = 1

	svc := geo.NewService(store, geo.NewValkeyCache(redisClient), geo.ServiceConfig{Logger: logger})
	handler := geo.NewHandler(svc, nil, logger)

	router := apphttp.NewRouter(apphttp.RouterConfig{
		CORSAllowedOrigins: []string{"http://localhost:3000"},
		Logger:             logger,
		MountPublic: func(public chi.Router) {
			handler.RegisterPublicRoutes(public)
		},
	})

	// Modo 1: Con GPS del jugador (-11.10679, -77.60881) y radio 15 km
	reqGPS := httptest.NewRequest(http.MethodGet, "/api/v1/public/complejos?lat=-11.10679&lng=-77.60881&radio_km=15&modalidad=6v6", nil)
	recGPS := httptest.NewRecorder()
	router.ServeHTTP(recGPS, reqGPS)
	if recGPS.Code != http.StatusOK {
		t.Fatalf("exploración con GPS status = %d, esperado 200; body=%s", recGPS.Code, recGPS.Body.String())
	}

	if !store.lastExploreQuery.UseGPS || store.lastExploreQuery.RadioMetros != 15000 {
		t.Fatalf("query GPS inesperada: %+v", store.lastExploreQuery)
	}

	var gpsEnvelope struct {
		Data geo.PublicComplejosListResponse `json:"data"`
	}
	if err := json.Unmarshal(recGPS.Body.Bytes(), &gpsEnvelope); err != nil {
		t.Fatalf("JSON inválido en modo GPS: %v", err)
	}
	if gpsEnvelope.Data.ModoBusqueda != geo.ModoBusquedaGPS {
		t.Errorf("modo_busqueda = %q, esperado GPS", gpsEnvelope.Data.ModoBusqueda)
	}
	if len(gpsEnvelope.Data.Items) != 1 || gpsEnvelope.Data.Items[0].DistanciaMetros == nil || *gpsEnvelope.Data.Items[0].DistanciaMetros != 342.55 {
		t.Errorf("distancia_metros inesperada en modo GPS: %+v", gpsEnvelope.Data.Items)
	}

	// Verificación estricta de privacidad (RN-19 / RNF-04): cero escrituras/lecturas en Valkey y cero coordenadas en logs
	if valkeySrv.sets.Load() != 0 || valkeySrv.gets.Load() != 0 {
		t.Fatalf("violación RN-19: exploración pública tocó Valkey (gets=%d, sets=%d)", valkeySrv.gets.Load(), valkeySrv.sets.Load())
	}
	logsStr := logBuf.String()
	if strings.Contains(logsStr, "-11.10679") || strings.Contains(logsStr, "-77.60881") {
		t.Fatalf("violación RNF-04: coordenadas GPS del jugador aparecieron en logs: %s", logsStr)
	}

	// Modo 2: Degradación elegante sin GPS (búsqueda por distrito y texto q)
	reqZona := httptest.NewRequest(http.MethodGet, "/api/v1/public/complejos?distrito=Huacho&q=Golazo", nil)
	recZona := httptest.NewRecorder()
	router.ServeHTTP(recZona, reqZona)
	if recZona.Code != http.StatusOK {
		t.Fatalf("exploración sin GPS status = %d, esperado 200; body=%s", recZona.Code, recZona.Body.String())
	}

	if store.lastExploreQuery.UseGPS {
		t.Fatal("UseGPS debía ser false cuando no se envían coordenadas")
	}
	if store.lastExploreQuery.Distrito != "Huacho" || store.lastExploreQuery.QueryLike != "%Golazo%" {
		t.Errorf("filtros de zona inesperados: %+v", store.lastExploreQuery)
	}
	if !bytes.Contains(recZona.Body.Bytes(), []byte(`"distancia_metros":null`)) {
		t.Errorf("sin GPS distancia_metros debe serializarse como null, body=%s", recZona.Body.String())
	}

	// Validaciones de entrada públicas -> 400
	badPublicURLs := []string{
		"/api/v1/public/complejos?lat=-11.1067",            // falta lng
		"/api/v1/public/complejos?lat=-11.1&lng=-77.6&radio_km=51", // radio > 50
		"/api/v1/public/complejos?limit=51",                // limit > 50
		"/api/v1/public/complejos?modalidad=10v10",         // modalidad no soportada
	}
	for _, u := range badPublicURLs {
		req := httptest.NewRequest(http.MethodGet, u, nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("URL pública inválida %s devolvió %d, esperado 400", u, rec.Code)
		}
	}
}

func TestPublicComplejoBySlug_RN02_ActiveSuspendedAndExpiredByDate(t *testing.T) {
	fixedNow := time.Date(2026, 11, 25, 18, 30, 0, 0, time.UTC)
	futureVence := fixedNow.Add(15 * 24 * time.Hour)
	pastVence := fixedNow.Add(-5 * time.Minute)

	lat := -11.1067
	lng := -77.6088
	dir := "Av. Grau 320, Huacho"
	tel := "987654321"
	terminos := "Llegar 10 minutos antes. No se permiten toperoles de aluminio."

	store := newFakeGeoStore()
	store.bySlug["cancha-vigente-huacho"] = geo.ComplejoBaseRecord{
		ID:                  uuid.New(),
		Slug:                "cancha-vigente-huacho",
		Nombre:              "Cancha Vigente Huacho",
		TelefonoContacto:    &tel,
		Servicios:           []string{"duchas", "wifi"},
		Departamento:        "Lima",
		Provincia:           "Huaura",
		Distrito:            "Huacho",
		Lat:                 &lat,
		Lng:                 &lng,
		DireccionEscrita:    &dir,
		Publicado:           true,
		UnidadReservaMin:    60,
		HoldDuracionMin:     10,
		TipoAdelanto:        "PORCENTAJE",
		ValorAdelanto:       50,
		HorasMinCancelacion: 24,
		TerminosReserva:     &terminos,
		EstadoSuscripcion:   geo.EstadoSuscripcionActiva,
		SuscripcionVenceAt:  &futureVence,
	}
	store.bySlug["cancha-suspendida-huacho"] = geo.ComplejoBaseRecord{
		ID:                 uuid.New(),
		Slug:               "cancha-suspendida-huacho",
		Nombre:             "Cancha Suspendida Huacho",
		Departamento:       "Lima",
		Provincia:          "Huaura",
		Distrito:           "Huacho",
		Publicado:          true,
		EstadoSuscripcion:  geo.EstadoSuscripcionSuspendida,
		SuscripcionVenceAt: &futureVence,
	}
	store.bySlug["cancha-vencida-fecha-huacho"] = geo.ComplejoBaseRecord{
		ID:                 uuid.New(),
		Slug:               "cancha-vencida-fecha-huacho",
		Nombre:             "Cancha Vencida Fecha Huacho",
		Departamento:       "Lima",
		Provincia:          "Huaura",
		Distrito:           "Huacho",
		Publicado:          true,
		EstadoSuscripcion:  geo.EstadoSuscripcionActiva,
		SuscripcionVenceAt: &pastVence,
	}
	store.bySlug["cancha-oculta"] = geo.ComplejoBaseRecord{
		ID:                 uuid.New(),
		Slug:               "cancha-oculta",
		Nombre:             "Cancha No Publicada",
		Publicado:          false,
		EstadoSuscripcion:  geo.EstadoSuscripcionActiva,
		SuscripcionVenceAt: &futureVence,
	}

	store.canchas = []geo.PublicCancha{
		{ID: uuid.New(), Nombre: "Cancha 1", Modalidad: "6v6", Superficie: "SINTETICO", Iluminacion: true, Orden: 1},
	}
	store.horarios = []geo.PublicHorarioAtencion{
		{DiaSemana: 1, MinutoApertura: 480, MinutoCierre: 1380, Cerrado: false},
	}
	store.tarifas = []geo.PublicTarifaFranja{
		{ID: uuid.New(), DiaSemana: 1, MinutoInicio: 480, MinutoFin: 1080, PrecioHora: 50},
	}
	store.tiposPago = []string{"PLIN", "YAPE"}

	valkeySrv, redisClient := startMiniValkeyServer(t)
	svc := geo.NewService(store, geo.NewValkeyCache(redisClient), geo.ServiceConfig{
		Now:    func() time.Time { return fixedNow },
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	handler := geo.NewHandler(svc, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	router := apphttp.NewRouter(apphttp.RouterConfig{
		CORSAllowedOrigins: []string{"http://localhost:3000"},
		Logger:             slog.New(slog.NewTextHandler(io.Discard, nil)),
		MountPublic: func(public chi.Router) {
			handler.RegisterPublicRoutes(public)
		},
	})

	// 1. No publicado o inexistente -> 404
	for _, slug := range []string{"no-existe", "cancha-oculta"} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/public/complejos/"+slug, nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("slug %q debía responder 404, obtenido %d", slug, rec.Code)
		}
	}

	// 2. Vigente -> 200 con acepta_reservas=true, catálogo completo y caché Valkey 60s
	reqVigente := httptest.NewRequest(http.MethodGet, "/api/v1/public/complejos/cancha-vigente-huacho", nil)
	recVigente := httptest.NewRecorder()
	router.ServeHTTP(recVigente, reqVigente)
	if recVigente.Code != http.StatusOK {
		t.Fatalf("vigente status = %d, esperado 200; body=%s", recVigente.Code, recVigente.Body.String())
	}

	rawBodyVigente := recVigente.Body.String()
	for _, forbidden := range []string{`"estado_suscripcion"`, `"suscripcion_vence_at"`, `"dueno_id"`, `"credenciales`, `"numero_destino"`, `"cci"`} {
		if strings.Contains(rawBodyVigente, forbidden) {
			t.Fatalf("la ficha pública filtró dato interno %s: %s", forbidden, rawBodyVigente)
		}
	}

	var fichaEnv struct {
		Data geo.PublicComplejoFicha `json:"data"`
	}
	if err := json.Unmarshal(recVigente.Body.Bytes(), &fichaEnv); err != nil {
		t.Fatalf("JSON inválido en ficha vigente: %v", err)
	}
	if !fichaEnv.Data.AceptaReservas || fichaEnv.Data.Motivo != nil {
		t.Fatalf("esperaba acepta_reservas=true y motivo=nil, obtenido %+v", fichaEnv.Data)
	}
	if fichaEnv.Data.ReglasReserva == nil || fichaEnv.Data.ReglasReserva.ValorAdelanto != 50 {
		t.Fatalf("reglas_reserva faltantes o incorrectas: %+v", fichaEnv.Data.ReglasReserva)
	}
	if len(fichaEnv.Data.TiposMetodosPago) != 2 || fichaEnv.Data.TiposMetodosPago[0] != "PLIN" {
		t.Fatalf("tipos_metodos_pago inesperados: %+v", fichaEnv.Data.TiposMetodosPago)
	}

	// Segunda llamada al mismo slug debe salir de caché Valkey (TTL 60s) sin volver al repositorio
	hitsBefore := store.slugHits
	recCached := httptest.NewRecorder()
	router.ServeHTTP(recCached, httptest.NewRequest(http.MethodGet, "/api/v1/public/complejos/cancha-vigente-huacho", nil))
	if recCached.Code != http.StatusOK || store.slugHits != hitsBefore {
		t.Fatalf("esperaba lectura desde caché Valkey por slug; slugHits antes=%d después=%d", hitsBefore, store.slugHits)
	}
	slugEntry, ok := valkeySrv.getEntry("geo:public:complejo:cancha-vigente-huacho")
	if !ok || slugEntry.ttlMs != 60000 {
		t.Fatalf("caché de slug esperada con TTL 60000ms, obtenido ok=%v entry=%+v", ok, slugEntry)
	}

	// Invalidación inmediata de caché por slug -> la siguiente consulta debe recargar desde repositorio
	if err := geo.NewValkeyCache(redisClient).InvalidatePublicComplejo(context.Background(), "cancha-vigente-huacho"); err != nil {
		t.Fatalf("InvalidatePublicComplejo falló: %v", err)
	}
	if _, stillCached := valkeySrv.getEntry("geo:public:complejo:cancha-vigente-huacho"); stillCached {
		t.Fatal("la clave de caché pública debía eliminarse inmediatamente tras InvalidatePublicComplejo")
	}
	store.fotos = []geo.PublicFoto{{ID: uuid.New(), URL: "/api/v1/public/media/public/complejos/1/fotos/nueva.jpg", EsPortada: true}}
	recAfterInvalidate := httptest.NewRecorder()
	router.ServeHTTP(recAfterInvalidate, httptest.NewRequest(http.MethodGet, "/api/v1/public/complejos/cancha-vigente-huacho", nil))
	if recAfterInvalidate.Code != http.StatusOK || store.slugHits != hitsBefore+1 {
		t.Fatalf("tras invalidación esperaba recarga desde repositorio; status=%d slugHits=%d esperado=%d", recAfterInvalidate.Code, store.slugHits, hitsBefore+1)
	}
	if !strings.Contains(recAfterInvalidate.Body.String(), "nueva.jpg") {
		t.Fatalf("la ficha recargada debía incluir la nueva foto: %s", recAfterInvalidate.Body.String())
	}

	// 3. Bloqueo RN-02 por estado SUSPENDIDA y por fecha vencida -> 200 con acepta_reservas=false y motivo claro
	for _, slug := range []string{"cancha-suspendida-huacho", "cancha-vencida-fecha-huacho"} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/public/complejos/"+slug, nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("slug bloqueado RN-02 %q debía devolver 200, obtenido %d", slug, rec.Code)
		}

		bodyStr := rec.Body.String()
		for _, forbidden := range []string{"SUSPENDIDA", "VENCIDA", "estado_suscripcion", "suscripcion_vence_at"} {
			if strings.Contains(bodyStr, forbidden) {
				t.Fatalf("slug %q expuso estado interno %q: %s", slug, forbidden, bodyStr)
			}
		}

		var env struct {
			Data geo.PublicComplejoFicha `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
			t.Fatalf("JSON inválido en %q: %v", slug, err)
		}
		if env.Data.AceptaReservas {
			t.Fatalf("slug %q debía devolver acepta_reservas=false", slug)
		}
		if env.Data.Motivo == nil || strings.TrimSpace(*env.Data.Motivo) == "" {
			t.Fatalf("slug %q debía devolver motivo claro de RN-02", slug)
		}
		if env.Data.ReglasReserva != nil || len(env.Data.Canchas) != 0 || len(env.Data.TiposMetodosPago) != 0 {
			t.Fatalf("slug %q no vigente no debe exponer catálogo de reserva: %+v", slug, env.Data)
		}
	}
}
