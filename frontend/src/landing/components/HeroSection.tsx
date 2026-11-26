"use client";

import { useEffect, useRef, useState } from "react";
import Link from "next/link";
import gsap from "gsap";
import { ScrollTrigger } from "gsap/ScrollTrigger";
import { SITE_CONFIG } from "@/shared/site-config";

const INTRO_STOP_TIME = 2.4;
const SCROLL_STATE_KEY = "fdp_landing_scroll_zone_v1";

type ViewportMediaVariant = "mobile" | "desktop";

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
  const sectionRef = useRef<HTMLElement | null>(null);
  const videoRef = useRef<HTMLVideoElement | null>(null);

  const act1Ref = useRef<HTMLDivElement | null>(null);
  const act2Ref = useRef<HTMLDivElement | null>(null);
  const act3Ref = useRef<HTMLDivElement | null>(null);

  const modeRef = useRef<"intro" | "scrub">("intro");
  const playheadRef = useRef({ progress: 0 });
  const renderedTimeRef = useRef(0);
  const introFinishedRef = useRef(false);

  /* null en SSR evita que el navegador descargue la fuente equivocada antes de evaluar matchMedia */
  const [mediaVariant, setMediaVariant] = useState<ViewportMediaVariant | null>(
    null
  );
  const [isVideoFrameReady, setIsVideoFrameReady] = useState(false);
  const [reducedMotion, setReducedMotion] = useState(false);
  const [counterValue, setCounterValue] = useState(1);
  const [isCounterVisible, setIsCounterVisible] = useState(true);
  const [isContentRevealed, setIsContentRevealed] = useState(false);

  /* Detección estricta de viewport (móvil <= 767px vs desktop >= 768px) y prefers-reduced-motion */
  useEffect(() => {
    const mobileQuery = window.matchMedia("(max-width: 767px)");
    const motionQuery = window.matchMedia("(prefers-reduced-motion: reduce)");

    const syncInitialViewport = () => {
      setMediaVariant(mobileQuery.matches ? "mobile" : "desktop");
    };

    const onViewportChange = (event: MediaQueryListEvent) => {
      setIsVideoFrameReady(false);
      setMediaVariant(event.matches ? "mobile" : "desktop");
    };

    const syncMotion = () => setReducedMotion(motionQuery.matches);

    syncInitialViewport();
    syncMotion();

    mobileQuery.addEventListener("change", onViewportChange);
    motionQuery.addEventListener("change", syncMotion);

    return () => {
      mobileQuery.removeEventListener("change", onViewportChange);
      motionQuery.removeEventListener("change", syncMotion);
    };
  }, []);

  /* Modo accesible reducido: fotograma estático en 2.4s sin contador ni autoplay */
  useEffect(() => {
    if (!reducedMotion || !mediaVariant) return;

    introFinishedRef.current = true;
    const rafId = window.requestAnimationFrame(() => {
      setIsCounterVisible(false);
      setIsContentRevealed(true);
    });

    const video = videoRef.current;
    if (!video) {
      return () => window.cancelAnimationFrame(rafId);
    }

    video.defaultMuted = true;
    video.muted = true;
    video.load();

    const seekStaticFrame = () => {
      video.pause();
      video.currentTime = INTRO_STOP_TIME;
      setIsVideoFrameReady(true);
    };

    if (video.readyState >= 2) {
      seekStaticFrame();
    } else {
      video.addEventListener("loadeddata", seekStaticFrame, { once: true });
    }

    return () => {
      window.cancelAnimationFrame(rafId);
      video.removeEventListener("loadeddata", seekStaticFrame);
    };
  }, [reducedMotion, mediaVariant]);

  /* Motor principal: carga adaptativa + Escenario A/B + scrubbing nítido sin tirones */
  useEffect(() => {
    if (reducedMotion || !mediaVariant || !sectionRef.current) return;

    if ("scrollRestoration" in window.history) {
      window.history.scrollRestoration = "manual";
    }

    gsap.registerPlugin(ScrollTrigger);
    ScrollTrigger.config({ ignoreMobileResize: true });

    const video = videoRef.current;
    const isDesktopViewport = mediaVariant === "desktop";
    const pinFactor = isDesktopViewport ? 2.2 : 1.6;
    const estimatedPinEndPx = Math.round(window.innerHeight * pinFactor);

    const savedState = readSavedScrollState();
    const initialBrowserScrollY = window.scrollY;
    const reloadedDocument = !hasMountedLandingInSpa && isDocumentReload();

    const isReloadInLowerSection =
      reloadedDocument &&
      (savedState?.zone === "lower" ||
        (!savedState && initialBrowserScrollY > estimatedPinEndPx + 24));

    const savedOffsetAfterPin =
      savedState?.zone === "lower"
        ? savedState.offsetAfterPin
        : Math.max(28, initialBrowserScrollY - estimatedPinEndPx);

    /* Inicializa siempre ScrollTrigger con scrollY = 0 para geometría exacta del pin */
    onImmediateScroll(0);

    let revealDelayTimer: ReturnType<typeof setTimeout> | undefined;
    let scrollTriggerInstance: ScrollTrigger | null = null;
    let isListeningScrollSave = false;

    const completeIntroAndRevealContent = (immediate = false) => {
      if (introFinishedRef.current) return;
      introFinishedRef.current = true;
      setCounterValue(100);
      setIsCounterVisible(false);

      if (immediate) {
        setIsContentRevealed(true);
      } else {
        revealDelayTimer = setTimeout(() => {
          setIsContentRevealed(true);
        }, 210);
      }
    };

    const ctx = gsap.context(() => {
      const mm = gsap.matchMedia();

      mm.add(
        {
          isDesktop: "(min-width: 768px)",
          isMobile: "(max-width: 767px)",
        },
        (mediaCtx) => {
          const isDesktop = Boolean(mediaCtx.conditions?.isDesktop);

          gsap.set(act1Ref.current, { autoAlpha: 1, y: 0 });
          gsap.set([act2Ref.current, act3Ref.current], {
            autoAlpha: 0,
            y: 22,
          });

          const tl = gsap.timeline({
            defaults: { ease: "none" },
            scrollTrigger: {
              trigger: sectionRef.current,
              start: "top top",
              end: isDesktop ? "+=220%" : "+=160%",
              pin: true,
              scrub: isDesktop ? 0.7 : 0.6,
              anticipatePin: 1,
              invalidateOnRefresh: true,
            },
          });

          scrollTriggerInstance = tl.scrollTrigger ?? null;

          tl.fromTo(
            playheadRef.current,
            { progress: 0 },
            { progress: 1, duration: 1, ease: "none" },
            0
          );

          /* ACTO 1 (0% – 28%) */
          tl.to(
            act1Ref.current,
            {
              autoAlpha: 0,
              y: -20,
              duration: 0.11,
              ease: "power2.inOut",
            },
            0.17
          );

          /* ACTO 2 (34% – 64%) */
          tl.fromTo(
            act2Ref.current,
            { autoAlpha: 0, y: 22 },
            {
              autoAlpha: 1,
              y: 0,
              duration: 0.09,
              ease: "power2.out",
            },
            0.34
          );
          tl.to(
            act2Ref.current,
            {
              autoAlpha: 0,
              y: -20,
              duration: 0.09,
              ease: "power2.inOut",
            },
            0.55
          );

          /* ACTO 3 (70% – 100%) */
          tl.fromTo(
            act3Ref.current,
            { autoAlpha: 0, y: 22 },
            {
              autoAlpha: 1,
              y: 0,
              duration: 0.1,
              ease: "power2.out",
            },
            0.7
          );
        }
      );
    }, sectionRef);

    const introBootClock = performance.now();

    const onVideoLoadedData = () => {
      setIsVideoFrameReady(true);
    };

    if (video) {
      video.defaultMuted = true;
      video.muted = true;
      video.playsInline = true;
      video.addEventListener("loadeddata", onVideoLoadedData);
      video.load();
    }

    const startHeroScenarioA = () => {
      onImmediateScroll(0);
      writeSavedScrollState({ zone: "hero", offsetAfterPin: 0 });

      introFinishedRef.current = false;
      modeRef.current = "intro";
      playheadRef.current.progress = 0;
      renderedTimeRef.current = 0;

      setCounterValue(1);
      setIsCounterVisible(true);
      setIsContentRevealed(false);

      if (!video) return;

      const launchNativeIntro = () => {
        if (modeRef.current !== "intro") return;
        setIsVideoFrameReady(true);
        video.playbackRate = 1;
        const playAttempt = video.play();
        if (playAttempt !== undefined) {
          playAttempt.catch(() => {
            modeRef.current = "scrub";
            renderedTimeRef.current = INTRO_STOP_TIME;
            video.pause();
            if (video.readyState >= 2) {
              video.currentTime = INTRO_STOP_TIME;
            }
            completeIntroAndRevealContent(false);
          });
        }
      };

      if (video.readyState >= 2) {
        launchNativeIntro();
      } else {
        video.addEventListener("canplay", launchNativeIntro, { once: true });
      }
    };

    const startLowerSectionScenarioB = () => {
      introFinishedRef.current = true;
      modeRef.current = "scrub";
      playheadRef.current.progress = 1;

      setCounterValue(100);
      setIsCounterVisible(false);
      setIsContentRevealed(true);

      const pinEnd = scrollTriggerInstance?.end ?? estimatedPinEndPx;
      const targetScrollY = Math.round(
        pinEnd + Math.max(28, savedOffsetAfterPin)
      );

      onImmediateScroll(targetScrollY);
      ScrollTrigger.update();

      const setFinalVideoFrame = () => {
        if (!video) return;
        setIsVideoFrameReady(true);
        video.pause();
        const endFrame = Math.max(
          INTRO_STOP_TIME,
          (video.duration || 12.8) - 0.04
        );
        renderedTimeRef.current = endFrame;
        video.currentTime = endFrame;
        ScrollTrigger.refresh();
        const updatedPinEnd = scrollTriggerInstance?.end ?? pinEnd;
        onImmediateScroll(
          Math.round(updatedPinEnd + Math.max(28, savedOffsetAfterPin))
        );
      };

      if (video) {
        if (video.readyState >= 2) {
          setFinalVideoFrame();
        } else {
          video.addEventListener("loadeddata", setFinalVideoFrame, {
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

    const persistCurrentZone = () => {
      if (!isListeningScrollSave) return;

      if (modeRef.current === "intro" && window.scrollY > 3) {
        modeRef.current = "scrub";
        if (video) {
          renderedTimeRef.current = video.currentTime;
          video.pause();
        }
        completeIntroAndRevealContent(true);
      }

      const activePinEnd =
        scrollTriggerInstance?.end ??
        Math.round(window.innerHeight * (window.innerWidth >= 768 ? 2.2 : 1.6));

      if (window.scrollY > activePinEnd + 24) {
        writeSavedScrollState({
          zone: "lower",
          offsetAfterPin: Math.round(window.scrollY - activePinEnd),
        });
      } else {
        writeSavedScrollState({ zone: "hero", offsetAfterPin: 0 });
      }
    };

    const onFirstMobileTouch = () => {
      if (!video || modeRef.current === "intro") return;
      if (video.paused && video.readyState >= 2) {
        const snapshot = renderedTimeRef.current || INTRO_STOP_TIME;
        const p = video.play();
        if (p !== undefined) {
          p.then(() => {
            video.pause();
            video.currentTime = snapshot;
          }).catch(() => {});
        }
      }
    };

    window.addEventListener("scroll", persistCurrentZone, { passive: true });
    window.addEventListener("pagehide", persistCurrentZone);
    window.addEventListener("touchstart", onFirstMobileTouch, {
      passive: true,
      once: true,
    });

    /* Bucle rAF vía gsap.ticker: intro nativa sin interferencia + scrubbing sin cola de seeks */
    const syncFrame = () => {
      const v = videoRef.current;

      if (modeRef.current === "intro") {
        const elapsedSec = (performance.now() - introBootClock) / 1000;
        const videoSec = v && v.readyState >= 2 ? v.currentTime : 0;
        const effectiveSec = Math.max(
          videoSec,
          Math.min(elapsedSec * 0.85, 0.32)
        );

        if (v && v.readyState >= 2) {
          renderedTimeRef.current = v.currentTime;
        }

        if (playheadRef.current.progress > 0.002) {
          modeRef.current = "scrub";
          if (v) {
            v.pause();
          }
          completeIntroAndRevealContent(true);
          return;
        }

        const rawPct = Math.min(
          100,
          Math.max(1, Math.ceil((effectiveSec / INTRO_STOP_TIME) * 100))
        );
        setCounterValue((prev) => (rawPct > prev ? rawPct : prev));

        if (v && v.readyState >= 2 && (v.currentTime >= INTRO_STOP_TIME || v.ended)) {
          v.pause();
          v.currentTime = INTRO_STOP_TIME;
          renderedTimeRef.current = INTRO_STOP_TIME;
          modeRef.current = "scrub";
          completeIntroAndRevealContent(false);
        }
        return;
      }

      if (
        !v ||
        v.readyState < 2 ||
        !Number.isFinite(v.duration) ||
        v.duration <= INTRO_STOP_TIME
      ) {
        return;
      }

      const maxTime = Math.max(INTRO_STOP_TIME, v.duration - 0.04);
      const desiredTime =
        INTRO_STOP_TIME +
        playheadRef.current.progress * (maxTime - INTRO_STOP_TIME);

      const delta = desiredTime - renderedTimeRef.current;
      if (Math.abs(delta) > 0.005) {
        renderedTimeRef.current += delta * 0.24;
      } else {
        renderedTimeRef.current = desiredTime;
      }

      const targetTime = Math.min(
        maxTime,
        Math.max(0, renderedTimeRef.current)
      );

      /* Solo busca fotograma si readyState >= 2, no está buscando y la diferencia >= 0.018s */
      if (!v.seeking && Math.abs(targetTime - v.currentTime) >= 0.018) {
        v.currentTime = targetTime;
      }
    };

    gsap.ticker.add(syncFrame);

    return () => {
      window.cancelAnimationFrame(enableTrackingRaf);
      if (revealDelayTimer) clearTimeout(revealDelayTimer);
      if (video) {
        video.removeEventListener("loadeddata", onVideoLoadedData);
      }
      window.removeEventListener("scroll", persistCurrentZone);
      window.removeEventListener("pagehide", persistCurrentZone);
      window.removeEventListener("touchstart", onFirstMobileTouch);
      gsap.ticker.remove(syncFrame);
      ctx.revert();
    };
  }, [reducedMotion, mediaVariant, onImmediateScroll]);

  const activePoster =
    mediaVariant === "mobile"
      ? SITE_CONFIG.media.heroMobilePoster
      : SITE_CONFIG.media.heroDesktopPoster;

  const activeWebm =
    mediaVariant === "mobile"
      ? SITE_CONFIG.media.heroMobileWebm
      : SITE_CONFIG.media.heroDesktopWebm;

  const activeMp4 =
    mediaVariant === "mobile"
      ? SITE_CONFIG.media.heroMobileMp4
      : SITE_CONFIG.media.heroDesktopMp4;

  return (
    <section
      ref={sectionRef}
      aria-label="Reserva canchas sintéticas en segundos"
      className="relative h-[100svh] w-full overflow-hidden bg-[#070B14] text-white md:h-[100dvh]"
    >
      {/* Primer pintado instantáneo en 4G (WebP vertical en móvil / WebP 1080p en desktop) */}
      <picture
        className={`pointer-events-none absolute inset-0 block h-full w-full select-none transition-opacity duration-300 ${
          isVideoFrameReady ? "opacity-0" : "opacity-100"
        }`}
      >
        <source
          media="(max-width: 767px)"
          srcSet={SITE_CONFIG.media.heroMobilePoster}
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

      {/* Video renderizado 1:1 sin filtros, blur, scale ni will-change */}
      <video
        ref={videoRef}
        muted
        playsInline
        preload="auto"
        poster={reducedMotion || !mediaVariant ? undefined : activePoster}
        aria-hidden="true"
        className="pointer-events-none absolute inset-0 h-full w-full object-cover object-center select-none"
      >
        {mediaVariant ? (
          <>
            <source src={activeWebm} type="video/webm" />
            <source src={activeMp4} type="video/mp4" />
          </>
        ) : null}
      </video>

      {/* Pluma superior discreta y degradado inferior suave en la banda baja */}
      <div
        aria-hidden="true"
        className="hero-top-feather pointer-events-none absolute inset-x-0 top-0 h-24 sm:h-28"
      />
      <div
        aria-hidden="true"
        className="hero-bottom-veil pointer-events-none absolute inset-x-0 bottom-0 h-[42%] sm:h-[38%]"
      />

      {/* Contador 1 -> 100 grande, grueso y con gradiente blanco -> gris plata elegante */}
      {!reducedMotion ? (
        <div
          aria-hidden={!isCounterVisible}
          className="pointer-events-none absolute bottom-10 left-1/2 z-20 -translate-x-1/2 select-none lg:right-12 lg:left-auto lg:translate-x-0"
        >
          <div
            data-visible={isCounterVisible ? "true" : "false"}
            className="hero-counter-inner flex items-baseline leading-none"
          >
            <span className="hero-counter-digits font-display text-[clamp(3.8rem,15vw,5.25rem)] font-extrabold tracking-[-0.045em] lg:text-[clamp(4.8rem,7vw,7.25rem)]">
              {counterValue}
            </span>
            <span className="hero-counter-percent font-display ml-1 text-[clamp(1.4rem,4.5vw,2rem)] font-bold tracking-[-0.02em] lg:text-[clamp(1.75rem,2.4vw,2.5rem)]">
              %
            </span>
          </div>
        </div>
      ) : null}

      {/* Banda inferior: centrada simétrica en móvil (< lg) y editorial baja en desktop (lg:) */}
      <div
        data-revealed={isContentRevealed ? "true" : "false"}
        className="hero-stage-reveal relative z-10 mx-auto flex h-full w-full max-w-7xl flex-col justify-end px-6 pb-10 sm:px-8 lg:px-12 lg:pb-12 xl:pb-14"
      >
        <div className="grid w-full grid-cols-1 grid-rows-1 items-end">
          {/* ACTO 1 (0% – 28% del scroll · entra limpio al terminar el 1 -> 100) */}
          <div
            ref={act1Ref}
            className="hero-copy-clarity col-start-1 row-start-1 mx-auto flex w-full max-w-xl flex-col items-center text-center lg:mx-0 lg:max-w-2xl lg:items-start lg:text-left"
          >
            <h1 className="font-display text-[clamp(1.95rem,4.4vw,3.75rem)] leading-[0.99] font-bold tracking-[-0.032em]">
              <span className="block text-white">
                Separa tu cancha en segundos.
              </span>
              <span className="mt-1 block text-white/75">
                Sin esperar por WhatsApp.
              </span>
            </h1>

            <p className="mt-3.5 max-w-md text-[15px] leading-relaxed text-white/80 sm:text-[16.5px] lg:max-w-lg">
              Horarios reales, precios claros y reserva directa.
            </p>

            <div className="mt-6 flex flex-col items-center gap-3 sm:flex-row sm:gap-4 lg:items-center">
              <Link
                href={SITE_CONFIG.routes.explore}
                className="cta-pure-btn inline-flex min-h-[48px] items-center justify-center gap-2.5 rounded-full bg-white px-6 py-3 text-[14.5px] font-semibold text-[#070B14] transition-colors duration-300 hover:bg-[#2563EB] hover:text-white focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-white sm:min-h-[50px] sm:px-7 sm:text-[15px]"
              >
                <span>Explorar canchas</span>
                <CtaArrowIcon />
              </Link>

              <span className="text-xs text-white/65 sm:text-[13px]">
                Sin crear cuenta
              </span>
            </div>
          </div>

          {/* ACTO 2 (34% – 64% del scroll · ascenso medio) */}
          {!reducedMotion ? (
            <div
              ref={act2Ref}
              className="hero-copy-clarity invisible col-start-1 row-start-1 mx-auto flex w-full max-w-xl flex-col items-center text-center opacity-0"
            >
              <h2 className="font-display text-[clamp(1.85rem,4.1vw,3.3rem)] leading-[1.01] font-bold tracking-[-0.032em] text-white">
                Congela tu turno al instante.
              </h2>

              <p className="mt-3 max-w-md text-[15px] leading-relaxed text-white/80 sm:text-[16.5px]">
                Paga directo con Yape o Plin. Cero cruces.
              </p>
            </div>
          ) : null}

          {/* ACTO 3 (70% – 100% del scroll · plano panorámico total) */}
          {!reducedMotion ? (
            <div
              ref={act3Ref}
              className="hero-copy-clarity invisible col-start-1 row-start-1 mx-auto flex w-full max-w-xl flex-col items-center text-center opacity-0"
            >
              <h2 className="font-display text-[clamp(1.85rem,4.1vw,3.3rem)] leading-[1.01] font-bold tracking-[-0.032em] text-white">
                Llega directo con tu QR.
              </h2>

              <p className="mt-3 max-w-md text-[15px] leading-relaxed text-white/80 sm:text-[16.5px]">
                Ruta exacta a la puerta y listo para jugar.
              </p>

              <div className="mt-6 flex justify-center">
                <Link
                  href={SITE_CONFIG.routes.explore}
                  className="cta-pure-btn inline-flex min-h-[48px] items-center justify-center gap-2.5 rounded-full bg-white px-6 py-3 text-[14.5px] font-semibold text-[#070B14] transition-colors duration-300 hover:bg-[#2563EB] hover:text-white focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-white sm:min-h-[50px] sm:px-7 sm:text-[15px]"
                >
                  <span>Explorar canchas</span>
                  <CtaArrowIcon />
                </Link>
              </div>
            </div>
          ) : null}
        </div>
      </div>
    </section>
  );
}
