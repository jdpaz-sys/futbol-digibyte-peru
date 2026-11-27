"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import Link from "next/link";
import gsap from "gsap";
import { ScrollTrigger } from "gsap/ScrollTrigger";
import { SITE_CONFIG } from "@/shared/site-config";

/* Keyframe exacto en Desktop (-g 6 a 24fps -> I-frame cada 0.25s; reposo intro exacto en 2.500000s) */
const INTRO_END_TIME = 2.5;
const DESKTOP_DAMPING_LAMBDA = 12;
const DESKTOP_SEEK_MIN_DELTA_SEC = 0.022;
const DESKTOP_SEEK_STUCK_TIMEOUT_MS = 180;
const SLOW_NETWORK_INTRO_TIMEOUT_SEC = 1.8;
const SCROLL_STATE_KEY = "fdp_landing_scroll_zone_v1";

type ViewportMediaVariant = "mobile" | "desktop";

interface NetworkInformationLike {
  saveData?: boolean;
  effectiveType?: string;
  addEventListener?: (type: string, listener: () => void) => void;
  removeEventListener?: (type: string, listener: () => void) => void;
}

type NavigatorWithConnection = Navigator & {
  connection?: NetworkInformationLike;
  mozConnection?: NetworkInformationLike;
  webkitConnection?: NetworkInformationLike;
};

const INLINE_WEBVIEW_VIDEO_ATTRS: Record<string, string> = {
  "webkit-playsinline": "true",
  "x5-playsinline": "true",
};

/**
 * Distingue recarga real del documento frente a navegación SPA interna.
 */
let hasMountedLandingInSpa = false;

function isDocumentReload(): boolean {
  if (typeof window === "undefined") return false;
  const navEntries = window.performance?.getEntriesByType?.("navigation");
  const nav = navEntries?.[0] as PerformanceNavigationTiming | undefined;
  return nav?.type === "reload";
}

function getNetworkConnection(): NetworkInformationLike | undefined {
  if (typeof navigator === "undefined") return undefined;
  const nav = navigator as NavigatorWithConnection;
  return nav.connection ?? nav.mozConnection ?? nav.webkitConnection;
}

function shouldConserveMobileData(): boolean {
  const conn = getNetworkConnection();
  if (!conn) return false;
  if (conn.saveData === true) return true;
  return conn.effectiveType === "slow-2g" || conn.effectiveType === "2g";
}

interface SavedScrollState {
  zone: "hero" | "lower";
  offsetAfterPin: number;
}

interface HeroSectionProps {
  onImmediateScroll: (targetY: number) => void;
}

function readSavedScrollState(): SavedScrollState | null {
  try {
    const raw = window.sessionStorage.getItem(SCROLL_STATE_KEY);
    if (!raw) return null;
    const parsed = JSON.parse(raw) as Partial<SavedScrollState>;
    if (parsed.zone === "lower" && typeof parsed.offsetAfterPin === "number") {
      return {
        zone: "lower",
        offsetAfterPin: Math.max(0, parsed.offsetAfterPin),
      };
    }
    if (parsed.zone === "hero") {
      return { zone: "hero", offsetAfterPin: 0 };
    }
    return null;
  } catch {
    return null;
  }
}

function writeSavedScrollState(state: SavedScrollState): void {
  try {
    window.sessionStorage.setItem(SCROLL_STATE_KEY, JSON.stringify(state));
  } catch {
    /* Ignorar modo privado restringido */
  }
}

function CtaArrowIcon() {
  return (
    <span aria-hidden="true" className="cta-arrow-stage">
      <svg
        viewBox="0 0 20 20"
        fill="none"
        className="cta-arrow-a h-4 w-4 stroke-current stroke-[2]"
      >
        <path
          d="M4 10H16M16 10L10.75 4.75M16 10L10.75 15.25"
          strokeLinecap="round"
          strokeLinejoin="round"
        />
      </svg>
      <svg
        viewBox="0 0 20 20"
        fill="none"
        className="cta-arrow-b h-4 w-4 stroke-current stroke-[2]"
      >
        <path
          d="M4 10H16M16 10L10.75 4.75M16 10L10.75 15.25"
          strokeLinecap="round"
          strokeLinejoin="round"
        />
      </svg>
    </span>
  );
}

export function HeroSection({ onImmediateScroll }: HeroSectionProps) {
  const trackRef = useRef<HTMLElement | null>(null);
  const videoRef = useRef<HTMLVideoElement | null>(null);

  const act1Ref = useRef<HTMLDivElement | null>(null);
  const act2Ref = useRef<HTMLDivElement | null>(null);
  const act3Ref = useRef<HTMLDivElement | null>(null);
  const exitParallaxRef = useRef<HTMLDivElement | null>(null);
  const counterDigitsRef = useRef<HTMLSpanElement | null>(null);

  const modeRef = useRef<"intro" | "scrub">("intro");
  const renderedTimeRef = useRef(0);
  const introFinishedRef = useRef(false);
  const videoFatalErrorRef = useRef(false);

  /* null en SSR evita doble descarga o hidratación desigual */
  const [mediaVariant, setMediaVariant] = useState<ViewportMediaVariant | null>(
    null
  );
  const [isMobileOrTouch, setIsMobileOrTouch] = useState(false);
  const [saveDataMode, setSaveDataMode] = useState(false);
  const [hasVideoError, setHasVideoError] = useState(false);
  const [isVideoFrameReady, setIsVideoFrameReady] = useState(false);
  const [reducedMotion, setReducedMotion] = useState(false);
  const [counterValue, setCounterValue] = useState(1);
  const [isCounterVisible, setIsCounterVisible] = useState(true);
  const [isContentRevealed, setIsContentRevealed] = useState(false);
  const [hasScrolledPastMobileCue, setHasScrolledPastMobileCue] =
    useState(false);

  /* Detección cliente de móvil/táctil (pointer: coarse o <= 767px), Save-Data y reduced-motion */
  useEffect(() => {
    const mobileQuery = window.matchMedia("(max-width: 767px)");
    const coarseQuery = window.matchMedia("(pointer: coarse)");
    const motionQuery = window.matchMedia("(prefers-reduced-motion: reduce)");
    const connection = getNetworkConnection();

    const evaluateDeviceMode = (resetVideoState: boolean) => {
      const isNarrowViewport = mobileQuery.matches;
      const touchOrMobile = isNarrowViewport || coarseQuery.matches;
      if (resetVideoState) {
        videoFatalErrorRef.current = false;
        setHasVideoError(false);
        setIsVideoFrameReady(false);
      }
      setIsMobileOrTouch(touchOrMobile);
      /* Selección de asset por ancho real (< 768px 608x1080 vertical vs >= 768px 1920x1080 16:9)
         evitando estirar el video vertical 608px en tablets/laptops táctiles apaisadas */
      setMediaVariant(isNarrowViewport ? "mobile" : "desktop");
    };

    const syncInitialClientEnv = () => {
      setSaveDataMode(shouldConserveMobileData());
      setReducedMotion(motionQuery.matches);
      evaluateDeviceMode(false);
    };

    const onDeviceMediaChange = () => evaluateDeviceMode(true);
    const syncMotion = () => setReducedMotion(motionQuery.matches);
    const syncNetwork = () => setSaveDataMode(shouldConserveMobileData());

    syncInitialClientEnv();

    mobileQuery.addEventListener("change", onDeviceMediaChange);
    coarseQuery.addEventListener("change", onDeviceMediaChange);
    motionQuery.addEventListener("change", syncMotion);
    connection?.addEventListener?.("change", syncNetwork);

    return () => {
      mobileQuery.removeEventListener("change", onDeviceMediaChange);
      coarseQuery.removeEventListener("change", onDeviceMediaChange);
      motionQuery.removeEventListener("change", syncMotion);
      connection?.removeEventListener?.("change", syncNetwork);
    };
  }, []);

  const shouldLoadVideo =
    mediaVariant !== null && !saveDataMode && !hasVideoError;

  /* H.264 High (-final +faststart): móvil autoplay (608x1080 1:1, -g 48 -bf 2) y desktop scrubbing (1920x1080 1:1, -g 6 -bf 0) */
  const resolvedVideoSrc = shouldLoadVideo
    ? mediaVariant === "mobile"
      ? SITE_CONFIG.media.heroMobileMp4
      : SITE_CONFIG.media.heroDesktopMp4
    : undefined;

  const handleVideoError = useCallback(() => {
    if (!resolvedVideoSrc) return;
    videoFatalErrorRef.current = true;
    setHasVideoError(true);
    setIsVideoFrameReady(false);
  }, [resolvedVideoSrc]);

  /* Modo accesible reducido: fotograma estático limpio + mensaje protagonista visible */
  useEffect(() => {
    if (!reducedMotion || !mediaVariant) return;

    introFinishedRef.current = true;
    const rafId = window.requestAnimationFrame(() => {
      setIsCounterVisible(false);
      setIsContentRevealed(true);
    });

    const video = videoRef.current;
    if (!video || !resolvedVideoSrc) {
      return () => window.cancelAnimationFrame(rafId);
    }

    video.defaultMuted = true;
    video.muted = true;
    video.playsInline = true;

    const revealSeekedStaticFrame = () => {
      if (!videoFatalErrorRef.current) {
        setIsVideoFrameReady(true);
      }
    };

    const seekStaticFrame = () => {
      video.pause();
      video.addEventListener("seeked", revealSeekedStaticFrame, { once: true });
      try {
        video.currentTime = INTRO_END_TIME;
      } catch {
        setIsVideoFrameReady(false);
      }
    };

    if (video.networkState === HTMLMediaElement.NETWORK_EMPTY) {
      video.load();
    }

    if (video.readyState >= 2) {
      seekStaticFrame();
    } else {
      video.addEventListener("loadeddata", seekStaticFrame, { once: true });
    }

    return () => {
      window.cancelAnimationFrame(rafId);
      video.removeEventListener("loadeddata", seekStaticFrame);
      video.removeEventListener("seeked", revealSeekedStaticFrame);
    };
  }, [reducedMotion, mediaVariant, resolvedVideoSrc]);

  /* =========================================================================
     RAMA 1 — MÓVIL / TÁCTIL (< 768px o pointer: coarse):
     Escenario limpio de 1 sola pantalla (100lvh/100svh), autoplay fluido en loop
     SIN scroll-scrubbing (cero temblores), cascada interpolada de las 3 frases
     sincronizada automáticamente con la reproducción del video, pausa inteligente
     con IntersectionObserver y señal discreta que se desvanece al bajar (> 24px).
     ========================================================================= */
  useEffect(() => {
    const trackEl = trackRef.current;
    if (reducedMotion || !mediaVariant || !isMobileOrTouch || !trackEl) return;

    if ("scrollRestoration" in window.history) {
      window.history.scrollRestoration = "manual";
    }

    gsap.registerPlugin(ScrollTrigger);
    ScrollTrigger.config({ ignoreMobileResize: true });

    const video = videoRef.current;
    const canUseVideo = Boolean(video && resolvedVideoSrc);

    const savedState = readSavedScrollState();
    const initialBrowserScrollY = window.scrollY;
    const reloadedDocument = !hasMountedLandingInSpa && isDocumentReload();
    const lowerThresholdPx = Math.round(window.innerHeight * 0.45);

    const isReloadInLowerSection =
      reloadedDocument &&
      (savedState?.zone === "lower" ||
        (!savedState && initialBrowserScrollY > lowerThresholdPx));

    const savedOffsetAfterHero =
      savedState?.zone === "lower"
        ? savedState.offsetAfterPin
        : Math.max(28, initialBrowserScrollY);

    let revealDelayTimer: ReturnType<typeof setTimeout> | undefined;
    let activeActTween: gsap.core.Timeline | null = null;
    let isListeningScrollSave = false;
    let isHeroIntersecting = true;
    let hasNativeVideoAdvanced = false;
    let mobileCueHiddenFlag = false;
    let currentActIndex = 0;
    let hasEnteredSecondActOnce = false;
    let syntheticLoopSec = INTRO_END_TIME;
    let lastPhraseSampleMs = performance.now();
    const introBootClock = performance.now();

    let clockFallbackState = {
      active: !canUseVideo,
      startMs: performance.now(),
      baseSec: 0,
    };

    const activateClockFallback = (currentSec: number) => {
      if (clockFallbackState.active) return;
      clockFallbackState = {
        active: true,
        startMs: performance.now(),
        baseSec: Math.min(INTRO_END_TIME, Math.max(0, currentSec)),
      };
    };

    const completeMobileIntro = (immediate = false) => {
      if (introFinishedRef.current) return;
      introFinishedRef.current = true;
      lastPhraseSampleMs = performance.now();
      setCounterValue(100);
      setIsCounterVisible(false);

      if (immediate) {
        setIsContentRevealed(true);
      } else {
        revealDelayTimer = setTimeout(() => {
          setIsContentRevealed(true);
        }, 180);
      }
    };

    const transitionToMobileAct = (nextIndex: number) => {
      if (nextIndex === currentActIndex) return;
      const actNodes = [act1Ref.current, act2Ref.current, act3Ref.current];
      const prevEl = actNodes[currentActIndex];
      const nextEl = actNodes[nextIndex];
      if (!nextEl) return;

      currentActIndex = nextIndex;
      activeActTween?.kill();

      actNodes.forEach((node, idx) => {
        if (node && idx !== nextIndex && node !== prevEl) {
          gsap.set(node, { autoAlpha: 0, y: 12 });
        }
      });

      const switchTl = gsap.timeline();
      if (prevEl) {
        switchTl.to(
          prevEl,
          {
            autoAlpha: 0,
            y: -12,
            duration: 0.42,
            ease: "power2.inOut",
          },
          0
        );
      }

      switchTl.fromTo(
        nextEl,
        { autoAlpha: 0, y: 12 },
        {
          autoAlpha: 1,
          y: 0,
          duration: 0.48,
          ease: "power2.out",
        },
        0.3
      );

      activeActTween = switchTl;
    };

    /* Sincroniza la cascada Acto 1 -> Acto 2 -> Acto 3 -> Acto 1 con el tiempo real del video (12.83s) */
    const syncMobilePhraseWithPlayback = () => {
      const now = performance.now();
      const dt = Math.min(0.5, Math.max(0, (now - lastPhraseSampleMs) / 1000));
      lastPhraseSampleMs = now;

      if (!isHeroIntersecting || !introFinishedRef.current) return;

      let playbackSec: number;
      if (
        video &&
        canUseVideo &&
        !videoFatalErrorRef.current &&
        !video.paused &&
        video.readyState >= 2
      ) {
        playbackSec = video.currentTime;
        syntheticLoopSec = playbackSec;
      } else {
        syntheticLoopSec = (syntheticLoopSec + dt) % 12.833333;
        playbackSec = syntheticLoopSec;
      }

      let targetAct: number;
      if (playbackSec >= 5.75 && playbackSec < 9.55) {
        hasEnteredSecondActOnce = true;
        targetAct = 1;
      } else if (
        playbackSec >= 9.55 ||
        (hasEnteredSecondActOnce && playbackSec < 1.15)
      ) {
        targetAct = 2;
      } else {
        targetAct = 0;
      }

      transitionToMobileAct(targetAct);
    };

    /* Estado inicial de los 3 actos + parallax suave de salida al subir el velo #070B14 */
    const ctx = gsap.context(() => {
      gsap.set(act1Ref.current, { autoAlpha: 1, y: 0 });
      gsap.set([act2Ref.current, act3Ref.current], {
        autoAlpha: 0,
        y: 12,
      });

      if (exitParallaxRef.current) {
        gsap.set(exitParallaxRef.current, { opacity: 1, y: 0 });
        gsap.fromTo(
          exitParallaxRef.current,
          { opacity: 1, y: 0 },
          {
            opacity: 0,
            y: -22,
            ease: "none",
            immediateRender: false,
            scrollTrigger: {
              trigger: trackEl,
              start: "bottom bottom",
              end: "bottom 46%",
              scrub: 0.18,
              invalidateOnRefresh: true,
            },
          }
        );
      }
    }, trackEl);

    const tryResumeAutoplay = () => {
      if (!video || !canUseVideo || videoFatalErrorRef.current) return;
      if (!isHeroIntersecting) return;
      video.muted = true;
      const playAttempt = video.play();
      if (playAttempt !== undefined) {
        playAttempt
          .then(() => {
            if (!isHeroIntersecting) {
              video.pause();
            } else {
              setIsVideoFrameReady(true);
            }
          })
          .catch(() => {
            /* Silencioso ante Low Power Mode / ahorro de batería */
            const elapsed = (performance.now() - introBootClock) / 1000;
            activateClockFallback(Math.min(elapsed * 0.22, 0.4));
          });
      }
    };

    const onVideoPlaying = () => {
      if (!video || videoFatalErrorRef.current) return;
      if (!isHeroIntersecting) {
        video.pause();
        return;
      }
      hasNativeVideoAdvanced = true;
      setIsVideoFrameReady(true);
    };

    const onVideoLoadedData = () => {
      if (!video || videoFatalErrorRef.current) return;
      setIsVideoFrameReady(true);
      if (isHeroIntersecting) {
        tryResumeAutoplay();
      }
    };

    if (video && canUseVideo) {
      video.defaultMuted = true;
      video.muted = true;
      video.loop = true;
      video.playsInline = true;
      video.setAttribute("muted", "");
      video.setAttribute("loop", "");
      video.setAttribute("playsinline", "");
      video.setAttribute("webkit-playsinline", "true");
      video.setAttribute("x5-playsinline", "true");
      video.addEventListener("playing", onVideoPlaying);
      video.addEventListener("loadeddata", onVideoLoadedData);
      video.addEventListener("timeupdate", syncMobilePhraseWithPlayback);
      video.load();
    }

    const phraseSyncTimer = window.setInterval(
      syncMobilePhraseWithPlayback,
      180
    );

    /* IntersectionObserver (threshold: 0.05): pausa automática al salir de vista y reanuda al volver */
    const heroVisibilityObserver = new IntersectionObserver(
      (entries) => {
        const entry = entries[0];
        if (!entry) return;
        isHeroIntersecting = entry.isIntersecting;
        lastPhraseSampleMs = performance.now();
        if (!video || !canUseVideo || videoFatalErrorRef.current) return;

        if (!entry.isIntersecting) {
          if (!video.paused) {
            video.pause();
          }
        } else if (video.paused) {
          tryResumeAutoplay();
        }
      },
      { threshold: 0.05 }
    );
    heroVisibilityObserver.observe(trackEl);

    const startMobileLowerRestore = () => {
      introFinishedRef.current = true;
      mobileCueHiddenFlag = true;
      setCounterValue(100);
      setIsCounterVisible(false);
      setIsContentRevealed(true);
      setHasScrolledPastMobileCue(true);
      onImmediateScroll(Math.round(savedOffsetAfterHero));
      ScrollTrigger.refresh();
      ScrollTrigger.update();
    };

    const startMobileTopIntro = () => {
      onImmediateScroll(0);
      writeSavedScrollState({ zone: "hero", offsetAfterPin: 0 });
      introFinishedRef.current = false;
      setCounterValue(1);
      setIsCounterVisible(true);
      setIsContentRevealed(false);
      setHasScrolledPastMobileCue(false);
      tryResumeAutoplay();
    };

    if (isReloadInLowerSection) {
      startMobileLowerRestore();
    } else {
      startMobileTopIntro();
    }

    const enableTrackingRaf = window.requestAnimationFrame(() => {
      hasMountedLandingInSpa = true;
      isListeningScrollSave = true;
    });

    const onMobileScroll = () => {
      const y = window.scrollY;
      const nextCueHidden = y > 24;
      if (nextCueHidden !== mobileCueHiddenFlag) {
        mobileCueHiddenFlag = nextCueHidden;
        setHasScrolledPastMobileCue(nextCueHidden);
      }

      if (!introFinishedRef.current && y > 8) {
        completeMobileIntro(true);
      }

      if (!isListeningScrollSave) return;
      if (y > lowerThresholdPx) {
        writeSavedScrollState({
          zone: "lower",
          offsetAfterPin: Math.round(y),
        });
      } else {
        writeSavedScrollState({ zone: "hero", offsetAfterPin: 0 });
      }
    };

    const onFirstTouchUnlock = () => {
      if (isHeroIntersecting && video && video.paused) {
        tryResumeAutoplay();
      }
    };

    window.addEventListener("scroll", onMobileScroll, { passive: true });
    window.addEventListener("pagehide", onMobileScroll);
    window.addEventListener("touchstart", onFirstTouchUnlock, {
      passive: true,
      once: true,
    });

    /* Reloj ligero solo durante los primeros ~2.5s del contador 1 -> 100 (jamás toca video.currentTime) */
    const tickMobileCounter = () => {
      if (introFinishedRef.current) return;

      const elapsedSec = (performance.now() - introBootClock) / 1000;
      const v = videoRef.current;
      const videoSec =
        v && canUseVideo && !videoFatalErrorRef.current && v.readyState >= 2
          ? v.currentTime
          : 0;

      if (v && !v.paused && videoSec > 0.03) {
        hasNativeVideoAdvanced = true;
      }

      const leadInSec = Math.min(elapsedSec * 0.22, 0.4);
      if (
        !clockFallbackState.active &&
        (videoFatalErrorRef.current ||
          (elapsedSec > SLOW_NETWORK_INTRO_TIMEOUT_SEC &&
            !hasNativeVideoAdvanced))
      ) {
        activateClockFallback(Math.max(videoSec, leadInSec));
      }

      const effectiveSec = clockFallbackState.active
        ? Math.min(
            INTRO_END_TIME,
            Math.max(
              videoSec,
              clockFallbackState.baseSec +
                ((performance.now() - clockFallbackState.startMs) / 1000) * 1.2
            )
          )
        : Math.max(videoSec, leadInSec);

      const rawPct = Math.min(
        100,
        Math.max(1, Math.ceil((effectiveSec / INTRO_END_TIME) * 100))
      );
      setCounterValue((prev) => (rawPct > prev ? rawPct : prev));

      if (effectiveSec >= INTRO_END_TIME) {
        completeMobileIntro(false);
        gsap.ticker.remove(tickMobileCounter);
      }
    };

    if (!introFinishedRef.current) {
      gsap.ticker.add(tickMobileCounter);
    }

    return () => {
      window.cancelAnimationFrame(enableTrackingRaf);
      window.clearInterval(phraseSyncTimer);
      if (revealDelayTimer) clearTimeout(revealDelayTimer);
      activeActTween?.kill();
      heroVisibilityObserver.disconnect();
      if (video) {
        video.removeEventListener("playing", onVideoPlaying);
        video.removeEventListener("loadeddata", onVideoLoadedData);
        video.removeEventListener("timeupdate", syncMobilePhraseWithPlayback);
      }
      window.removeEventListener("scroll", onMobileScroll);
      window.removeEventListener("pagehide", onMobileScroll);
      window.removeEventListener("touchstart", onFirstTouchUnlock);
      gsap.ticker.remove(tickMobileCounter);
      ctx.revert();
    };
  }, [
    reducedMotion,
    mediaVariant,
    isMobileOrTouch,
    resolvedVideoSrc,
    onImmediateScroll,
  ]);

  /* =========================================================================
     RAMA 2 — ESCRITORIO (>= 768px con pointer: fine):
     Motor de scroll-scrubbing directo sobre ruta estática preload="auto"
     (-g 6, I-frame cada 0.25s), amortiguación exponencial limpia y seguro
     anti-bloqueo de seeking (> 180ms).
     ========================================================================= */
  useEffect(() => {
    const trackEl = trackRef.current;
    if (reducedMotion || !mediaVariant || isMobileOrTouch || !trackEl) return;

    if ("scrollRestoration" in window.history) {
      window.history.scrollRestoration = "manual";
    }

    gsap.registerPlugin(ScrollTrigger);
    ScrollTrigger.config({ ignoreMobileResize: true });

    const video = videoRef.current;
    const canUseVideo = Boolean(video && resolvedVideoSrc);

    const measureInitialTrackScrollEndPx = () => {
      const measured = trackEl.offsetHeight - window.innerHeight;
      if (measured > 60) return Math.round(measured);
      const fallbackFactor = window.innerWidth >= 1024 ? 1.45 : 1.35;
      return Math.round(window.innerHeight * fallbackFactor);
    };

    const estimatedTrackEndPx = measureInitialTrackScrollEndPx();
    const estimatedLowerThresholdPx =
      estimatedTrackEndPx + Math.round(window.innerHeight * 0.45);
    const savedState = readSavedScrollState();
    const initialBrowserScrollY = window.scrollY;
    const reloadedDocument = !hasMountedLandingInSpa && isDocumentReload();

    const isReloadInLowerSection =
      reloadedDocument &&
      (savedState?.zone === "lower" ||
        (!savedState && initialBrowserScrollY > estimatedLowerThresholdPx));

    const savedOffsetAfterPin =
      savedState?.zone === "lower"
        ? savedState.offsetAfterPin
        : Math.max(28, initialBrowserScrollY - estimatedTrackEndPx);

    onImmediateScroll(0);

    let revealDelayTimer: ReturnType<typeof setTimeout> | undefined;
    let scrollTriggerInstance: ScrollTrigger | null = null;
    let actsTimeline: gsap.core.Timeline | null = null;
    let isListeningScrollSave = false;
    let hasPrimedInteraction = false;
    let hasNativeVideoAdvanced = false;
    let desktopFrameReadyFlag = false;
    let desktopSettledExactApplied = false;
    let isHeroNearViewport = true;
    let rawProgress = 0;
    let smoothProgress = 0;
    let lastAppliedActsProgress = -1;
    let lastSeekIssuedMs = 0;
    let lastRenderedCounterPct = 1;
    let lastPersistedZone: "hero" | "lower" | null = null;
    let lastPersistedLowerBucket = -1;

    let clockFallbackState = {
      active: !canUseVideo,
      startMs: performance.now(),
      baseSec: 0,
    };

    const markDesktopFrameReady = () => {
      if (desktopFrameReadyFlag || videoFatalErrorRef.current) return;
      desktopFrameReadyFlag = true;
      setIsVideoFrameReady(true);
    };

    const updateDesktopCounterDom = (pct: number) => {
      if (pct <= lastRenderedCounterPct) return;
      lastRenderedCounterPct = pct;
      if (counterDigitsRef.current) {
        counterDigitsRef.current.textContent = String(pct);
      }
    };

    const activateClockFallback = (currentSec: number) => {
      if (clockFallbackState.active) return;
      clockFallbackState = {
        active: true,
        startMs: performance.now(),
        baseSec: Math.min(INTRO_END_TIME, Math.max(0, currentSec)),
      };
    };

    const removeInteractionPrimers = () => {
      window.removeEventListener("pointerdown", unlockVideoOnFirstInteraction);
      window.removeEventListener("wheel", unlockVideoOnFirstInteraction);
    };

    const completeIntroAndRevealContent = (immediate = false) => {
      if (introFinishedRef.current) return;
      introFinishedRef.current = true;
      removeInteractionPrimers();
      lastRenderedCounterPct = 100;
      if (counterDigitsRef.current) {
        counterDigitsRef.current.textContent = "100";
      }
      setCounterValue(100);
      setIsCounterVisible(false);

      if (immediate) {
        if (revealDelayTimer) {
          clearTimeout(revealDelayTimer);
          revealDelayTimer = undefined;
        }
        setIsContentRevealed(true);
      } else {
        revealDelayTimer = setTimeout(() => {
          revealDelayTimer = undefined;
          setIsContentRevealed(true);
        }, 180);
      }
    };

    const ctx = gsap.context(() => {
      /* Compuerta ligera de proximidad para desactivar cálculos cuando el Hero queda atrás */
      ScrollTrigger.create({
        trigger: trackEl,
        start: "top bottom",
        end: "bottom -35%",
        onToggle: (self) => {
          isHeroNearViewport = self.isActive;
          if (!self.isActive && modeRef.current === "scrub") {
            smoothProgress = rawProgress;
            if (
              actsTimeline &&
              Math.abs(smoothProgress - lastAppliedActsProgress) > 0.00004
            ) {
              actsTimeline.progress(smoothProgress);
              lastAppliedActsProgress = smoothProgress;
            }
          }
        },
        onRefresh: (self) => {
          isHeroNearViewport = self.isActive;
        },
      });

      gsap.set(act1Ref.current, { autoAlpha: 1, y: 0, force3D: true });
      gsap.set([act2Ref.current, act3Ref.current], {
        autoAlpha: 0,
        y: 22,
        force3D: true,
      });

      /* Timeline pausada gobernada directamente por smoothProgress en el ticker */
      const tl = gsap.timeline({
        paused: true,
        defaults: { ease: "none" },
      });
      actsTimeline = tl;

      /* Ancla duración normalizada exacta [0..1] para conservar intactos los tiempos de los 3 actos */
      tl.to({}, { duration: 1 }, 0);

      tl.to(
        act1Ref.current,
        {
          autoAlpha: 0,
          y: -20,
          force3D: true,
          duration: 0.1,
          ease: "power2.inOut",
        },
        0.16
      );

      tl.fromTo(
        act2Ref.current,
        { autoAlpha: 0, y: 22, force3D: true },
        {
          autoAlpha: 1,
          y: 0,
          force3D: true,
          duration: 0.09,
          ease: "power2.out",
        },
        0.32
      );
      tl.to(
        act2Ref.current,
        {
          autoAlpha: 0,
          y: -20,
          force3D: true,
          duration: 0.09,
          ease: "power2.inOut",
        },
        0.54
      );

      tl.fromTo(
        act3Ref.current,
        { autoAlpha: 0, y: 22, force3D: true },
        {
          autoAlpha: 1,
          y: 0,
          force3D: true,
          duration: 0.09,
          ease: "power2.out",
        },
        0.69
      );

      scrollTriggerInstance = ScrollTrigger.create({
        trigger: trackEl,
        start: "top top",
        end: "bottom bottom",
        pin: false,
        scrub: true,
        invalidateOnRefresh: true,
        onUpdate: (self) => {
          rawProgress = self.progress;
        },
        onRefresh: (self) => {
          rawProgress = self.progress;
        },
      });

      if (exitParallaxRef.current) {
        gsap.set(exitParallaxRef.current, { opacity: 1, y: 0, force3D: true });
        gsap.fromTo(
          exitParallaxRef.current,
          { opacity: 1, y: 0, force3D: true },
          {
            opacity: 0,
            y: -36,
            force3D: true,
            ease: "none",
            immediateRender: false,
            scrollTrigger: {
              trigger: trackEl,
              start: "bottom bottom",
              end: "bottom 42%",
              scrub: true,
              invalidateOnRefresh: true,
            },
          }
        );
      }
    }, trackEl);

    const introBootClock = performance.now();

    let pendingCanPlayHandler: (() => void) | null = null;
    let pendingLoadedDataHandler: (() => void) | null = null;

    const onVideoPlaying = () => {
      if (!video || videoFatalErrorRef.current) return;
      if (modeRef.current !== "intro") {
        video.pause();
        return;
      }
      hasNativeVideoAdvanced = true;
      removeInteractionPrimers();
      markDesktopFrameReady();
    };

    const onVideoSeeked = () => {
      lastSeekIssuedMs = 0;
      if (!video || videoFatalErrorRef.current) return;
      if (video.readyState >= 2) {
        markDesktopFrameReady();
      }
    };

    const onVideoLoadedData = () => {
      if (!video || videoFatalErrorRef.current) return;
      if (video.readyState >= 2) {
        markDesktopFrameReady();
      }
      if (modeRef.current === "scrub") {
        video.pause();
        desktopSettledExactApplied = false;
        const dur =
          Number.isFinite(video.duration) && video.duration > INTRO_END_TIME
            ? video.duration
            : 12.875;
        const maxSeekTime = Math.max(INTRO_END_TIME, dur - 0.04);
        const syncedTime = Math.min(
          maxSeekTime,
          Math.max(
            INTRO_END_TIME,
            INTRO_END_TIME + smoothProgress * (dur - INTRO_END_TIME)
          )
        );
        renderedTimeRef.current = syncedTime;
        try {
          lastSeekIssuedMs = performance.now();
          video.currentTime = syncedTime;
        } catch {
          /* Reintentará en el siguiente tick */
        }
      }
    };

    if (video && canUseVideo) {
      video.defaultMuted = true;
      video.muted = true;
      video.loop = false;
      video.playsInline = true;
      video.setAttribute("muted", "");
      video.setAttribute("playsinline", "");
      video.setAttribute("webkit-playsinline", "true");
      video.setAttribute("x5-playsinline", "true");
      video.addEventListener("playing", onVideoPlaying);
      video.addEventListener("seeked", onVideoSeeked);
      video.addEventListener("loadeddata", onVideoLoadedData);
      if (video.networkState === HTMLMediaElement.NETWORK_EMPTY) {
        video.load();
      }
      if (video.readyState >= 2) {
        markDesktopFrameReady();
      }
    }

    const startHeroScenarioA = () => {
      onImmediateScroll(0);
      writeSavedScrollState({ zone: "hero", offsetAfterPin: 0 });
      lastPersistedZone = "hero";
      lastPersistedLowerBucket = -1;

      introFinishedRef.current = false;
      modeRef.current = "intro";
      rawProgress = 0;
      smoothProgress = 0;
      desktopSettledExactApplied = false;
      lastSeekIssuedMs = 0;
      renderedTimeRef.current = 0;
      actsTimeline?.progress(0);
      lastAppliedActsProgress = 0;
      lastRenderedCounterPct = 1;
      if (counterDigitsRef.current) {
        counterDigitsRef.current.textContent = "1";
      }

      setCounterValue(1);
      setIsCounterVisible(true);
      setIsContentRevealed(false);

      if (!video || !canUseVideo) {
        activateClockFallback(0);
        return;
      }

      const launchNativeIntro = () => {
        pendingCanPlayHandler = null;
        if (modeRef.current !== "intro" || videoFatalErrorRef.current) return;
        video.playbackRate = 1;
        const playAttempt = video.play();
        if (playAttempt !== undefined) {
          playAttempt
            .then(() => {
              if (modeRef.current !== "intro") {
                video.pause();
              } else {
                hasNativeVideoAdvanced = true;
                removeInteractionPrimers();
                markDesktopFrameReady();
              }
            })
            .catch(() => {
              video.pause();
              const elapsed = (performance.now() - introBootClock) / 1000;
              activateClockFallback(Math.min(elapsed * 0.2, 0.36));
            });
        }
      };

      if (video.readyState >= 2) {
        launchNativeIntro();
      } else {
        pendingCanPlayHandler = launchNativeIntro;
        video.addEventListener("canplay", launchNativeIntro, { once: true });
      }
    };

    const startLowerSectionScenarioB = () => {
      introFinishedRef.current = true;
      modeRef.current = "scrub";
      rawProgress = 1;
      smoothProgress = 1;
      desktopSettledExactApplied = false;
      actsTimeline?.progress(1);
      lastAppliedActsProgress = 1;
      removeInteractionPrimers();

      lastRenderedCounterPct = 100;
      if (counterDigitsRef.current) {
        counterDigitsRef.current.textContent = "100";
      }
      setCounterValue(100);
      setIsCounterVisible(false);
      setIsContentRevealed(true);

      const trackEnd = scrollTriggerInstance?.end ?? estimatedTrackEndPx;
      const targetScrollY = Math.round(
        trackEnd + Math.max(28, savedOffsetAfterPin)
      );
      lastPersistedZone = "lower";
      lastPersistedLowerBucket = Math.floor(
        Math.max(28, savedOffsetAfterPin) / 240
      );

      onImmediateScroll(targetScrollY);
      ScrollTrigger.refresh();
      ScrollTrigger.update();

      const setFinalVideoFrame = () => {
        if (!video || !canUseVideo || videoFatalErrorRef.current) return;
        video.pause();
        const dur = Number.isFinite(video.duration) && video.duration > INTRO_END_TIME
          ? video.duration
          : 12.875;
        const endTime = Math.max(INTRO_END_TIME, dur - 0.04);
        renderedTimeRef.current = endTime;
        try {
          lastSeekIssuedMs = performance.now();
          video.currentTime = endTime;
          markDesktopFrameReady();
        } catch {
          /* Poster permanece visible si el seek inicial no está disponible */
        }
        ScrollTrigger.refresh();
        if (!hasPrimedInteraction) {
          const updatedTrackEnd =
            scrollTriggerInstance?.end ?? estimatedTrackEndPx;
          onImmediateScroll(
            Math.round(updatedTrackEnd + Math.max(28, savedOffsetAfterPin))
          );
          ScrollTrigger.update();
        }
      };

      if (video && canUseVideo) {
        if (video.readyState >= 2) {
          setFinalVideoFrame();
        } else {
          const handleOnceLoadedData = () => {
            pendingLoadedDataHandler = null;
            setFinalVideoFrame();
          };
          pendingLoadedDataHandler = handleOnceLoadedData;
          video.addEventListener("loadeddata", handleOnceLoadedData, {
            once: true,
          });
        }
      }

      if (document.fonts?.ready) {
        document.fonts.ready.then(() => {
          ScrollTrigger.refresh();
        });
      }
    };

    if (isReloadInLowerSection) {
      startLowerSectionScenarioB();
    } else {
      startHeroScenarioA();
    }

    const enableTrackingRaf = window.requestAnimationFrame(() => {
      hasMountedLandingInSpa = true;
      isListeningScrollSave = true;
    });

    const transitionIntroToScrubImmediate = () => {
      modeRef.current = "scrub";
      desktopSettledExactApplied = false;
      if (video && canUseVideo) {
        const currentSec =
          video.readyState >= 2 && video.currentTime > 0
            ? video.currentTime
            : INTRO_END_TIME;
        renderedTimeRef.current = Math.max(INTRO_END_TIME, currentSec);
        video.pause();
      } else {
        renderedTimeRef.current = INTRO_END_TIME;
      }
      completeIntroAndRevealContent(true);
    };

    const persistCurrentZone = (forceFlush = false) => {
      if (!isListeningScrollSave) return;

      const currentY = window.scrollY;

      if (modeRef.current === "intro" && currentY > 3) {
        transitionIntroToScrubImmediate();
      }

      const activeTrackEnd = scrollTriggerInstance?.end ?? estimatedTrackEndPx;
      const lowerZoneThresholdPx =
        activeTrackEnd + Math.round(window.innerHeight * 0.45);

      if (currentY > lowerZoneThresholdPx) {
        const offsetAfterPin = Math.round(currentY - activeTrackEnd);
        const bucket = Math.floor(offsetAfterPin / 240);
        if (
          forceFlush ||
          lastPersistedZone !== "lower" ||
          bucket !== lastPersistedLowerBucket
        ) {
          lastPersistedZone = "lower";
          lastPersistedLowerBucket = bucket;
          writeSavedScrollState({ zone: "lower", offsetAfterPin });
        }
      } else if (lastPersistedZone !== "hero") {
        lastPersistedZone = "hero";
        lastPersistedLowerBucket = -1;
        writeSavedScrollState({ zone: "hero", offsetAfterPin: 0 });
      }
    };

    const onDesktopScroll = () => persistCurrentZone(false);
    const onDesktopPageHide = () => persistCurrentZone(true);

    /* Solo desbloquea reproducción si el navegador bloqueó el autoplay del intro (jamás en modo scrub) */
    function unlockVideoOnFirstInteraction() {
      if (hasPrimedInteraction) return;
      hasPrimedInteraction = true;
      removeInteractionPrimers();

      if (!video || !canUseVideo || videoFatalErrorRef.current) return;
      if (modeRef.current !== "intro" || hasNativeVideoAdvanced || !video.paused) {
        return;
      }

      video.muted = true;
      const playPromise = video.play();
      if (playPromise !== undefined) {
        playPromise
          .then(() => {
            if (modeRef.current !== "intro") {
              video.pause();
            } else {
              hasNativeVideoAdvanced = true;
              markDesktopFrameReady();
            }
          })
          .catch(() => {
            /* Fallback de reloj continúa limpiamente si el entorno restringe autoplay */
          });
      }
    }

    window.addEventListener("scroll", onDesktopScroll, { passive: true });
    window.addEventListener("pagehide", onDesktopPageHide);
    window.addEventListener("pointerdown", unlockVideoOnFirstInteraction, {
      passive: true,
      once: true,
    });
    window.addEventListener("wheel", unlockVideoOnFirstInteraction, {
      passive: true,
      once: true,
    });

    /* Ticker sincronizado: amortiguación exponencial 1 - exp(-12 * dt) + seguro anti-bloqueo de seeking */
    const syncFrame = (_timeSec: number, deltaTimeMs: number) => {
      const v = videoRef.current;

      if (scrollTriggerInstance) {
        rawProgress = scrollTriggerInstance.progress;
      }

      if (modeRef.current === "intro") {
        const elapsedSec = (performance.now() - introBootClock) / 1000;
        const videoSec =
          v && canUseVideo && !videoFatalErrorRef.current && v.readyState >= 2
            ? v.currentTime
            : 0;

        if (v && !v.paused && videoSec > 0.03) {
          hasNativeVideoAdvanced = true;
          markDesktopFrameReady();
        }

        if (v && canUseVideo && v.readyState >= 2 && videoSec > 0) {
          renderedTimeRef.current = videoSec;
        }

        if (rawProgress > 0.002) {
          transitionIntroToScrubImmediate();
        } else {
          const leadInSec = Math.min(elapsedSec * 0.2, 0.36);

          if (
            !clockFallbackState.active &&
            (videoFatalErrorRef.current ||
              (elapsedSec > SLOW_NETWORK_INTRO_TIMEOUT_SEC &&
                !hasNativeVideoAdvanced))
          ) {
            activateClockFallback(Math.max(videoSec, leadInSec));
          }

          const effectiveSec = clockFallbackState.active
            ? Math.min(
                INTRO_END_TIME,
                Math.max(
                  videoSec,
                  clockFallbackState.baseSec +
                    ((performance.now() - clockFallbackState.startMs) / 1000) *
                      1.15
                )
              )
            : Math.max(videoSec, leadInSec);

          const rawPct = Math.min(
            100,
            Math.max(1, Math.ceil((effectiveSec / INTRO_END_TIME) * 100))
          );
          updateDesktopCounterDom(rawPct);

          const nativeReachedStop =
            Boolean(v && canUseVideo && v.readyState >= 2) &&
            ((v?.currentTime ?? 0) >= INTRO_END_TIME || Boolean(v?.ended));

          const fallbackReachedStop =
            clockFallbackState.active && effectiveSec >= INTRO_END_TIME;

          if (nativeReachedStop || fallbackReachedStop) {
            if (v && canUseVideo) {
              v.pause();
              if (v.readyState >= 2 && !videoFatalErrorRef.current) {
                try {
                  /* Fija exactamente el I-frame de reposo en 2.500000s (-g 6) */
                  lastSeekIssuedMs = performance.now();
                  v.currentTime = INTRO_END_TIME;
                  markDesktopFrameReady();
                } catch {
                  /* Conserva poster si el seek aún no está disponible */
                }
              }
            }
            renderedTimeRef.current = INTRO_END_TIME;
            desktopSettledExactApplied = false;
            modeRef.current = "scrub";
            completeIntroAndRevealContent(false);
          }
          return;
        }
      }

      if (!isHeroNearViewport) {
        return;
      }

      const dtSeconds = Math.min(
        0.05,
        Math.max(0.001, (deltaTimeMs || 16.67) / 1000)
      );
      const progressDelta = rawProgress - smoothProgress;
      const isProgressSettled = Math.abs(progressDelta) <= 0.00015;

      if (isProgressSettled) {
        smoothProgress = rawProgress;
      } else {
        desktopSettledExactApplied = false;
        const factor = 1 - Math.exp(-DESKTOP_DAMPING_LAMBDA * dtSeconds);
        smoothProgress += progressDelta * factor;
      }

      if (
        actsTimeline &&
        Math.abs(smoothProgress - lastAppliedActsProgress) > 0.00004
      ) {
        actsTimeline.progress(smoothProgress);
        lastAppliedActsProgress = smoothProgress;
      }

      if (
        !v ||
        !canUseVideo ||
        videoFatalErrorRef.current ||
        v.readyState < 1 ||
        !Number.isFinite(v.duration) ||
        v.duration <= INTRO_END_TIME
      ) {
        return;
      }

      if (!v.paused) {
        v.pause();
      }

      const maxSeekTime = Math.max(INTRO_END_TIME, v.duration - 0.04);
      const targetTime = Math.min(
        maxSeekTime,
        Math.max(
          INTRO_END_TIME,
          INTRO_END_TIME + smoothProgress * (v.duration - INTRO_END_TIME)
        )
      );
      renderedTimeRef.current = targetTime;

      const nowMs = performance.now();
      if (v.seeking && lastSeekIssuedMs === 0) {
        lastSeekIssuedMs = nowMs;
      } else if (!v.seeking && lastSeekIssuedMs !== 0) {
        lastSeekIssuedMs = 0;
      }

      /* Seguro anti-bloqueo: normal exige readyState >= 2 && !seeking; si seeking > 180ms sin seeked, desbloquea */
      const isNormalReady = v.readyState >= 2 && !v.seeking;
      const isSeekStuck =
        v.seeking &&
        lastSeekIssuedMs > 0 &&
        nowMs - lastSeekIssuedMs > DESKTOP_SEEK_STUCK_TIMEOUT_MS;
      const canSeekNow = isNormalReady || isSeekStuck;
      const timeDiff = Math.abs(targetTime - v.currentTime);

      if (!isProgressSettled) {
        if (canSeekNow && timeDiff >= DESKTOP_SEEK_MIN_DELTA_SEC) {
          lastSeekIssuedMs = nowMs;
          v.currentTime = targetTime;
        }
      } else if (!desktopSettledExactApplied && canSeekNow) {
        if (timeDiff >= 0.012) {
          lastSeekIssuedMs = nowMs;
          v.currentTime = targetTime;
        }
        desktopSettledExactApplied = true;
      }
    };

    gsap.ticker.add(syncFrame);

    return () => {
      window.cancelAnimationFrame(enableTrackingRaf);
      if (revealDelayTimer) clearTimeout(revealDelayTimer);

      if (video) {
        video.removeEventListener("playing", onVideoPlaying);
        video.removeEventListener("seeked", onVideoSeeked);
        video.removeEventListener("loadeddata", onVideoLoadedData);
        if (pendingCanPlayHandler) {
          video.removeEventListener("canplay", pendingCanPlayHandler);
        }
        if (pendingLoadedDataHandler) {
          video.removeEventListener("loadeddata", pendingLoadedDataHandler);
        }
      }

      window.removeEventListener("scroll", onDesktopScroll);
      window.removeEventListener("pagehide", onDesktopPageHide);
      removeInteractionPrimers();
      gsap.ticker.remove(syncFrame);
      ctx.revert();
    };
  }, [
    reducedMotion,
    mediaVariant,
    isMobileOrTouch,
    resolvedVideoSrc,
    onImmediateScroll,
  ]);

  const activePoster =
    mediaVariant === "mobile"
      ? SITE_CONFIG.media.heroMobilePoster
      : SITE_CONFIG.media.heroDesktopPoster;

  const showPosterOverlay =
    !isVideoFrameReady || hasVideoError || saveDataMode;

  const showMobileScrollCue =
    isMobileOrTouch &&
    !reducedMotion &&
    isContentRevealed &&
    !hasScrolledPastMobileCue;

  return (
    <section
      ref={trackRef}
      aria-label="Reserva canchas sintéticas en segundos"
      className={
        reducedMotion
          ? "hero-reduced-track relative w-full bg-[#070B14] text-white"
          : isMobileOrTouch
            ? "hero-touch-track relative w-full bg-[#070B14] text-white"
            : "hero-scroll-track relative w-full bg-[#070B14] text-white"
      }
    >
      {/* Escenario sticky/fullscreen nativo en GPU (100lvh con fallback 100vh) */}
      <div className="hero-sticky-stage sticky top-0 w-full overflow-hidden bg-[#070B14]">
        {/* Video de fondo 1:1 nítido (autoPlay loop fluido en móvil; scrubbing directo en escritorio) */}
        <video
          ref={videoRef}
          src={resolvedVideoSrc}
          autoPlay={isMobileOrTouch && shouldLoadVideo && !reducedMotion}
          loop={isMobileOrTouch}
          muted
          playsInline
          preload={shouldLoadVideo ? "auto" : "none"}
          disablePictureInPicture
          disableRemotePlayback
          poster={!shouldLoadVideo || reducedMotion ? undefined : activePoster}
          onError={handleVideoError}
          aria-hidden="true"
          {...INLINE_WEBVIEW_VIDEO_ATTRS}
          className={`hero-gpu-video pointer-events-none absolute inset-0 z-0 h-full w-full object-cover object-center select-none ${
            hasVideoError || saveDataMode ? "hidden" : ""
          }`}
        />

        {/* Primer pintado instantáneo WebP (-final) con fetchPriority="high" */}
        <picture
          className={`pointer-events-none absolute inset-0 z-[1] block h-full w-full select-none transition-opacity duration-300 ${
            showPosterOverlay ? "opacity-100" : "opacity-0"
          }`}
        >
          <source
            media="(max-width: 767px)"
            srcSet={SITE_CONFIG.media.heroMobilePoster}
            type="image/webp"
          />
          <source
            media="(min-width: 768px)"
            srcSet={SITE_CONFIG.media.heroDesktopPoster}
            type="image/webp"
          />
          <img
            src={SITE_CONFIG.media.heroDesktopPoster}
            alt=""
            aria-hidden="true"
            fetchPriority="high"
            decoding="sync"
            className="h-full w-full object-cover object-center"
          />
        </picture>

        {/* Degradado inferior sutil localizado detrás del texto */}
        <div
          aria-hidden="true"
          className="hero-bottom-veil pointer-events-none absolute inset-x-0 bottom-0 z-[2] h-[40%] sm:h-[30%]"
        />

        {/* Capa segura 100svh */}
        <div className="hero-safe-ui-layer pointer-events-none absolute inset-x-0 top-0 z-10 w-full">
          {/* Contador 1 -> 100 */}
          {!reducedMotion ? (
            <div
              aria-hidden={!isCounterVisible}
              className="pointer-events-none absolute bottom-[max(2.25rem,env(safe-area-inset-bottom))] left-1/2 z-20 -translate-x-1/2 select-none lg:right-12 lg:bottom-10 lg:left-auto lg:translate-x-0"
            >
              <div
                data-visible={isCounterVisible ? "true" : "false"}
                className="hero-counter-inner flex items-baseline leading-none"
              >
                <span
                  ref={counterDigitsRef}
                  className="hero-counter-digits font-display text-[clamp(3.8rem,15vw,5.25rem)] font-extrabold tracking-[-0.045em] lg:text-[clamp(4.8rem,7vw,7.25rem)]"
                >
                  {counterValue}
                </span>
                <span className="hero-counter-percent font-display ml-1 text-[clamp(1.4rem,4.5vw,2rem)] font-bold tracking-[-0.02em] lg:text-[clamp(1.75rem,2.4vw,2.5rem)]">
                  %
                </span>
              </div>
            </div>
          ) : null}

          {/* Contenido protagonista */}
          <div
            data-revealed={isContentRevealed ? "true" : "false"}
            className={`hero-stage-reveal relative z-10 mx-auto flex h-full w-full max-w-7xl flex-col justify-end px-6 sm:px-8 lg:px-12 lg:pb-12 xl:pb-14 ${
              isMobileOrTouch
                ? "pb-[max(5.35rem,calc(env(safe-area-inset-bottom)+4.35rem))]"
                : "pb-[max(2.25rem,env(safe-area-inset-bottom))]"
            }`}
          >
            <div ref={exitParallaxRef} className="flex w-full flex-col">
              <div className="grid w-full grid-cols-1 grid-rows-1 items-end">
                {/* ACTO 1 (primer acto en cascada tanto en móvil como en desktop) */}
                <div
                  ref={act1Ref}
                  className="hero-copy-clarity hero-pc-act-layer col-start-1 row-start-1 mx-auto flex w-full max-w-xl flex-col items-center text-center lg:mx-0 lg:max-w-[30rem] lg:items-start lg:text-left xl:max-w-[34rem]"
                >
                  <h1 className="font-display text-[clamp(1.95rem,4.4vw,3.75rem)] leading-[0.99] font-bold tracking-[-0.032em] lg:text-[3rem] lg:leading-[1.02] lg:tracking-[-0.03em] xl:text-[3.5rem] 2xl:text-[3.85rem]">
                    <span className="hero-pc-title-primary block text-white">
                      Separa tu cancha en segundos.
                    </span>
                    <span className="hero-pc-title-secondary mt-1 block text-white/75 lg:mt-1.5">
                      Sin esperar por WhatsApp.
                    </span>
                  </h1>

                  <p className="mt-3.5 max-w-lg text-[15px] leading-relaxed text-white/80 sm:text-[16.5px]">
                    Mira horarios libres, compara precios y reserva al instante.
                  </p>

                  {!isMobileOrTouch ? (
                    <div className="mt-6 flex flex-col items-center gap-3 sm:flex-row sm:gap-4 lg:items-center">
                      <Link
                        href={SITE_CONFIG.routes.explore}
                        className="cta-pure-btn inline-flex min-h-[48px] items-center justify-center gap-2.5 rounded-full bg-white px-6 py-3 text-[14.5px] font-semibold text-[#070B14] transition-colors duration-300 hover:bg-[#2563EB] hover:text-white focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-white sm:min-h-[50px] sm:px-7 sm:text-[15px]"
                      >
                        <span>Ver canchas disponibles</span>
                        <CtaArrowIcon />
                      </Link>

                      <span className="text-xs text-white/65 sm:text-[13px]">
                        Sin crear cuenta
                      </span>
                    </div>
                  ) : null}
                </div>

                {/* ACTO 2 (en bucle automático en móvil; scrubbing en desktop) */}
                {!reducedMotion ? (
                  <div
                    ref={act2Ref}
                    className="hero-copy-clarity hero-pc-act-layer invisible col-start-1 row-start-1 mx-auto flex w-full max-w-xl flex-col items-center text-center opacity-0 lg:max-w-3xl xl:max-w-4xl"
                  >
                    <h2 className="hero-pc-title-primary font-display text-[clamp(1.9rem,5.8vw,3.3rem)] leading-[1.04] font-bold tracking-[-0.032em] text-white sm:text-[clamp(1.85rem,4.1vw,3.3rem)] sm:leading-[1.02] lg:text-[3rem] lg:leading-[1.02] lg:tracking-[-0.03em] xl:text-[3.5rem] 2xl:text-[3.85rem]">
                      Paga tu adelanto con Yape o Plin.
                    </h2>

                    <p className="mt-3 max-w-lg text-[15px] leading-relaxed text-white/80 sm:text-[16.5px]">
                      Mientras pagas, apartamos tu horario para que nadie más lo
                      tome.
                    </p>
                  </div>
                ) : null}

                {/* ACTO 3 (en bucle automático en móvil; scrubbing en desktop) */}
                {!reducedMotion ? (
                  <div
                    ref={act3Ref}
                    className="hero-copy-clarity hero-pc-act-layer invisible col-start-1 row-start-1 mx-auto flex w-full max-w-xl flex-col items-center text-center opacity-0 lg:max-w-3xl xl:max-w-4xl"
                  >
                    <h2 className="hero-pc-title-primary font-display text-[clamp(1.9rem,5.8vw,3.3rem)] leading-[1.04] font-bold tracking-[-0.032em] text-white sm:text-[clamp(1.85rem,4.1vw,3.3rem)] sm:leading-[1.02] lg:text-[3rem] lg:leading-[1.02] lg:tracking-[-0.03em] xl:text-[3.5rem] 2xl:text-[3.85rem]">
                      Tu cancha asegurada antes de salir.
                    </h2>

                    <p className="mt-3 max-w-lg text-[15px] leading-relaxed text-white/80 sm:text-[16.5px]">
                      Recibe tu pase digital de entrada con código QR y entra
                      directo a jugar.
                    </p>

                    {!isMobileOrTouch ? (
                      <div className="mt-6 flex justify-center">
                        <Link
                          href={SITE_CONFIG.routes.explore}
                          className="cta-pure-btn inline-flex min-h-[48px] items-center justify-center gap-2.5 rounded-full bg-white px-6 py-3 text-[14.5px] font-semibold text-[#070B14] transition-colors duration-300 hover:bg-[#2563EB] hover:text-white focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-white sm:min-h-[50px] sm:px-7 sm:text-[15px]"
                        >
                          <span>Ver canchas disponibles</span>
                          <CtaArrowIcon />
                        </Link>
                      </div>
                    ) : null}
                  </div>
                ) : null}
              </div>

              {/* CTA fijo exclusivo de móvil: siempre presente con aire equilibrado (mt-7) bajo las 3 frases */}
              {isMobileOrTouch ? (
                <div className="mt-7 flex flex-col items-center gap-2">
                  <Link
                    href={SITE_CONFIG.routes.explore}
                    className="cta-pure-btn inline-flex min-h-[48px] items-center justify-center gap-2.5 rounded-full bg-white px-6 py-3 text-[14.5px] font-semibold text-[#070B14] transition-colors duration-300 hover:bg-[#2563EB] hover:text-white focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-white"
                  >
                    <span>Ver canchas disponibles</span>
                    <CtaArrowIcon />
                  </Link>

                  <span className="hero-copy-clarity text-xs text-white/65">
                    Sin crear cuenta
                  </span>
                </div>
              ) : null}
            </div>
          </div>

          {/* Señal minimalista no invasiva solo en móvil: se desvanece suavemente apenas baja (> 24px) */}
          {isMobileOrTouch && !reducedMotion ? (
            <div
              aria-hidden="true"
              className={`pointer-events-none absolute inset-x-0 bottom-[max(0.75rem,env(safe-area-inset-bottom))] z-20 flex flex-col items-center gap-1.5 transition-opacity duration-300 ${
                showMobileScrollCue ? "opacity-100" : "opacity-0"
              }`}
            >
              <span className="tabular-mono pl-[0.2em] text-[11px] font-medium tracking-[0.2em] text-white/55 uppercase">
                Desliza para explorar
              </span>
              <span className="relative h-6 w-px overflow-hidden bg-white/20">
                <span className="hero-mobile-scroll-dot absolute top-0 left-1/2 -ml-[1.5px] h-[5px] w-[3px] rounded-full bg-white" />
              </span>
            </div>
          ) : null}
        </div>
      </div>
    </section>
  );
}
