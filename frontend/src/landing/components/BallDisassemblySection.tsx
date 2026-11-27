"use client";

import type { MouseEvent } from "react";
import { useCallback, useEffect, useRef, useState } from "react";
import Link from "next/link";
import gsap from "gsap";
import { ScrollTrigger } from "gsap/ScrollTrigger";
import { SITE_CONFIG } from "@/shared/site-config";

const FALLBACK_BALL_DURATION_SEC = 15.416667;
const MOBILE_SEEK_MIN_INTERVAL_MS = 36;
const MOBILE_SEEK_MIN_DELTA_SEC = 0.03;
const MOBILE_SCRUB_LERP = 0.2;
const DESKTOP_SCRUB_LERP = 0.18;
const DESKTOP_SEEK_MIN_INTERVAL_MS = 34;
const DESKTOP_SEEK_MIN_DELTA_SEC = 0.025;

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

interface BallDisassemblySectionProps {
  onScrollToSection?: (targetSelector: string) => void;
}

function EditorialArrowLink({
  href,
  label,
}: {
  href: string;
  label: string;
}) {
  return (
    <Link
      href={href}
      className="editorial-action-link inline-flex items-center gap-2 py-1 text-[14.5px] font-semibold text-[#070B14] transition-colors duration-250 hover:text-[#2563EB] focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-[#2563EB] sm:text-[15.5px]"
    >
      <span>{label}</span>
      <span aria-hidden="true" className="editorial-inline-arrow">
        →
      </span>
    </Link>
  );
}

export function BallDisassemblySection({
  onScrollToSection,
}: BallDisassemblySectionProps) {
  const sectionRef = useRef<HTMLElement | null>(null);
  const videoRef = useRef<HTMLVideoElement | null>(null);
  const edgeSealRef = useRef<HTMLDivElement | null>(null);
  const videoFrameBoxRef = useRef<HTMLDivElement | null>(null);
  const exitStageRef = useRef<HTMLDivElement | null>(null);

  const act1Ref = useRef<HTMLDivElement | null>(null);
  const act2Ref = useRef<HTMLDivElement | null>(null);
  const act3Ref = useRef<HTMLDivElement | null>(null);

  const seg1FillRef = useRef<HTMLSpanElement | null>(null);
  const seg2FillRef = useRef<HTMLSpanElement | null>(null);
  const seg3FillRef = useRef<HTMLSpanElement | null>(null);

  const playheadRef = useRef({ progress: 0 });
  const renderedTimeRef = useRef(0);
  const videoFatalErrorRef = useRef(false);
  const isNearViewportRef = useRef(false);
  const canUseBallVideoRef = useRef(false);

  const [mediaVariant, setMediaVariant] = useState<ViewportMediaVariant | null>(
    null
  );
  const [isMobileOrTouch, setIsMobileOrTouch] = useState(false);
  const [saveDataMode, setSaveDataMode] = useState(false);
  const [hasVideoError, setHasVideoError] = useState(false);
  const [isVideoFrameReady, setIsVideoFrameReady] = useState(false);
  const [reducedMotion, setReducedMotion] = useState(false);
  const [isStudioWhite, setIsStudioWhite] = useState(false);
  const [isBallMediaActivated, setIsBallMediaActivated] = useState(false);

  /* Detección adaptativa real (móvil <= 767px o pointer: coarse vs desktop) + Save-Data + reduced-motion */
  useEffect(() => {
    const mobileQuery = window.matchMedia("(max-width: 767px)");
    const coarseQuery = window.matchMedia("(pointer: coarse)");
    const motionQuery = window.matchMedia("(prefers-reduced-motion: reduce)");
    const connection = getNetworkConnection();

    const evaluateDeviceMode = (resetVideoState: boolean) => {
      const touchOrMobile = mobileQuery.matches || coarseQuery.matches;
      if (resetVideoState) {
        videoFatalErrorRef.current = false;
        setHasVideoError(false);
        setIsVideoFrameReady(false);
      }
      setIsMobileOrTouch(touchOrMobile);
      setMediaVariant(touchOrMobile ? "mobile" : "desktop");
    };

    const syncInitialClientEnv = () => {
      setSaveDataMode(shouldConserveMobileData());
      setReducedMotion(motionQuery.matches);
      evaluateDeviceMode(false);
    };

    const onDeviceChange = () => evaluateDeviceMode(true);
    const syncMotion = () => setReducedMotion(motionQuery.matches);
    const syncNetwork = () => setSaveDataMode(shouldConserveMobileData());

    syncInitialClientEnv();

    mobileQuery.addEventListener("change", onDeviceChange);
    coarseQuery.addEventListener("change", onDeviceChange);
    motionQuery.addEventListener("change", syncMotion);
    connection?.addEventListener?.("change", syncNetwork);

    return () => {
      mobileQuery.removeEventListener("change", onDeviceChange);
      coarseQuery.removeEventListener("change", onDeviceChange);
      motionQuery.removeEventListener("change", syncMotion);
      connection?.removeEventListener?.("change", syncNetwork);
    };
  }, []);

  /* Lazy-buffer del balón (15 MB): mientras el usuario está en el Hero (scroll 0),
     NO descarga ni instancia decodificadores H.264 en paralelo. Se activa con rootMargin '700px 0px'. */
  useEffect(() => {
    if (isBallMediaActivated) return;
    const sectionEl = sectionRef.current;
    if (!sectionEl) return;

    if (typeof IntersectionObserver === "undefined") {
      const rafId = window.requestAnimationFrame(() => {
        setIsBallMediaActivated(true);
      });
      return () => window.cancelAnimationFrame(rafId);
    }

    const preloadObserver = new IntersectionObserver(
      (entries) => {
        if (entries.some((entry) => entry.isIntersecting)) {
          setIsBallMediaActivated(true);
          preloadObserver.disconnect();
        }
      },
      { rootMargin: "700px 0px" }
    );

    preloadObserver.observe(sectionEl);

    return () => {
      preloadObserver.disconnect();
    };
  }, [isBallMediaActivated]);

  const shouldLoadVideo =
    mediaVariant !== null &&
    isBallMediaActivated &&
    !saveDataMode &&
    !hasVideoError;

  const resolvedVideoSrc = shouldLoadVideo
    ? isMobileOrTouch || mediaVariant === "mobile"
      ? SITE_CONFIG.media.ballMobileMp4
      : SITE_CONFIG.media.ballDesktopMp4
    : undefined;

  useEffect(() => {
    canUseBallVideoRef.current = Boolean(resolvedVideoSrc);
  }, [resolvedVideoSrc]);

  const handleVideoError = useCallback(() => {
    if (!resolvedVideoSrc) return;
    videoFatalErrorRef.current = true;
    setHasVideoError(true);
    setIsVideoFrameReady(false);
  }, [resolvedVideoSrc]);

  const handleOwnersScroll = useCallback(
    (event: MouseEvent<HTMLAnchorElement>) => {
      if (!onScrollToSection) return;
      const target = document.querySelector(SITE_CONFIG.routes.ownersAnchor);
      if (!target) return;
      event.preventDefault();
      onScrollToSection(SITE_CONFIG.routes.ownersAnchor);
    },
    [onScrollToSection]
  );

  /* Modo accesible reducido: muestra fotograma estático del balón desarmado + los 3 actos legibles */
  useEffect(() => {
    if (!reducedMotion || !mediaVariant) return;

    const video = videoRef.current;
    if (!video || !resolvedVideoSrc) return;

    video.defaultMuted = true;
    video.muted = true;
    video.playsInline = true;

    const onSeeked = () => {
      if (!videoFatalErrorRef.current) {
        setIsVideoFrameReady(true);
        setIsStudioWhite(true);
      }
    };

    const seekShowcaseFrame = () => {
      video.pause();
      video.addEventListener("seeked", onSeeked, { once: true });
      try {
        const targetSec = Math.min(
          11.6,
          Math.max(0, (video.duration || FALLBACK_BALL_DURATION_SEC) * 0.76)
        );
        video.currentTime = targetSec;
      } catch {
        setIsVideoFrameReady(false);
      }
    };

    video.load();
    if (video.readyState >= 2) {
      seekShowcaseFrame();
    } else {
      video.addEventListener("loadeddata", seekShowcaseFrame, { once: true });
    }

    return () => {
      video.removeEventListener("loadeddata", seekShowcaseFrame);
      video.removeEventListener("seeked", onSeeked);
    };
  }, [reducedMotion, mediaVariant, resolvedVideoSrc]);

  /* Hidratación diferida del <video> del balón: solo corre cuando el usuario se acerca (700px) */
  useEffect(() => {
    if (reducedMotion || !mediaVariant || !resolvedVideoSrc) return;

    const video = videoRef.current;
    if (!video) return;

    let ballFrameReadyFlag = false;
    let hasPrimedInteraction = false;

    const markBallFrameReady = () => {
      if (ballFrameReadyFlag || videoFatalErrorRef.current) return;
      ballFrameReadyFlag = true;
      setIsVideoFrameReady(true);
    };

    const onVideoSeeked = () => {
      if (!video || videoFatalErrorRef.current) return;
      if (video.readyState >= 2) {
        markBallFrameReady();
        video.removeEventListener("seeked", onVideoSeeked);
      }
    };

    const onVideoLoadedData = () => {
      if (!video || videoFatalErrorRef.current) return;
      video.pause();
      const maxTime = Math.max(
        0,
        (video.duration || FALLBACK_BALL_DURATION_SEC) - 0.04
      );
      const syncedTime = Math.min(
        maxTime,
        Math.max(0, playheadRef.current.progress * maxTime)
      );
      renderedTimeRef.current = syncedTime;
      setIsStudioWhite(playheadRef.current.progress >= 0.22);

      try {
        video.currentTime = syncedTime;
        if (video.readyState >= 2) {
          markBallFrameReady();
        }
      } catch {
        /* Reintentará en el siguiente ciclo de scroll */
      }
    };

    video.defaultMuted = true;
    video.muted = true;
    video.playsInline = true;
    video.setAttribute("muted", "");
    video.setAttribute("playsinline", "");
    video.setAttribute("webkit-playsinline", "true");
    video.setAttribute("x5-playsinline", "true");
    video.addEventListener("seeked", onVideoSeeked);
    video.addEventListener("loadeddata", onVideoLoadedData);
    video.load();

    const removeInteractionPrimers = () => {
      window.removeEventListener("touchstart", unlockBallVideo);
      window.removeEventListener("pointerdown", unlockBallVideo);
    };

    function unlockBallVideo() {
      if (hasPrimedInteraction) return;
      hasPrimedInteraction = true;
      removeInteractionPrimers();

      if (!video || videoFatalErrorRef.current) return;
      if (video.readyState >= 2 && ballFrameReadyFlag) return;

      video.muted = true;
      const snapshotTime = renderedTimeRef.current || 0;

      const playPromise = video.play();
      if (playPromise !== undefined) {
        playPromise
          .then(() => {
            video.pause();
            if (video.readyState >= 2) {
              video.currentTime = snapshotTime;
              markBallFrameReady();
            }
          })
          .catch(() => {
            if (video.readyState >= 2) {
              try {
                video.currentTime = snapshotTime;
              } catch {
                /* Mantiene poster HD si el entorno restringe decodificación */
              }
            }
          });
      }
    }

    if (isMobileOrTouch) {
      window.addEventListener("touchstart", unlockBallVideo, {
        passive: true,
        once: true,
      });
      window.addEventListener("pointerdown", unlockBallVideo, {
        passive: true,
        once: true,
      });
    }

    return () => {
      video.removeEventListener("seeked", onVideoSeeked);
      video.removeEventListener("loadeddata", onVideoLoadedData);
      removeInteractionPrimers();
    };
  }, [reducedMotion, mediaVariant, isMobileOrTouch, resolvedVideoSrc]);

  /* Motor principal de scroll-scrubbing GPU (pin: false) sobre blanco puro #FFFFFF (cero capa gris intermedia) */
  useEffect(() => {
    const sectionEl = sectionRef.current;
    if (reducedMotion || !mediaVariant || !sectionEl) return;

    gsap.registerPlugin(ScrollTrigger);
    ScrollTrigger.config({ ignoreMobileResize: true });

    let lastMobileSeekMs = 0;
    let lastDesktopSeekMs = 0;
    let mobileSettledExactApplied = false;
    let desktopSettledExactApplied = false;
    let studioWhiteFlag = false;

    const updateStudioWhiteFlag = (nextWhite: boolean) => {
      if (studioWhiteFlag === nextWhite) return;
      studioWhiteFlag = nextWhite;
      setIsStudioWhite(nextWhite);
    };

    const snapVideoToRestedFrame = () => {
      const v = videoRef.current;
      if (
        !v ||
        !canUseBallVideoRef.current ||
        videoFatalErrorRef.current ||
        v.readyState < 2 ||
        !Number.isFinite(v.duration) ||
        v.duration <= 0.1
      ) {
        return;
      }

      const maxTime = Math.max(0, v.duration - 0.04);
      const exactDesiredTime = Math.min(
        maxTime,
        Math.max(0, playheadRef.current.progress * maxTime)
      );
      renderedTimeRef.current = exactDesiredTime;

      const applyExactSeek = () => {
        if (
          v.readyState < 2 ||
          v.seeking ||
          Math.abs(exactDesiredTime - v.currentTime) <= 0.012
        ) {
          mobileSettledExactApplied = true;
          desktopSettledExactApplied = true;
          return;
        }
        const now = performance.now();
        lastMobileSeekMs = now;
        lastDesktopSeekMs = now;
        v.currentTime = exactDesiredTime;
        mobileSettledExactApplied = true;
        desktopSettledExactApplied = true;
      };

      if (v.seeking) {
        v.addEventListener("seeked", applyExactSeek, { once: true });
      } else {
        applyExactSeek();
      }
    };

    const ctx = gsap.context(() => {
      /* Vigilancia ligera de proximidad para no consumir rAF fuera de escena */
      ScrollTrigger.create({
        trigger: sectionEl,
        start: "top 140%",
        end: "bottom -40%",
        onToggle: (self) => {
          isNearViewportRef.current = self.isActive;
        },
        onRefresh: (self) => {
          isNearViewportRef.current = self.isActive;
        },
      });

      /* Estado inicial limpio sobre #FFFFFF directo (cero cortina gris; micro-desplazamiento suave 10px igual que Dueños) */
      gsap.set(act1Ref.current, { autoAlpha: 1, y: 0 });
      gsap.set([act2Ref.current, act3Ref.current], { autoAlpha: 0, y: 10 });
      gsap.set(
        [seg1FillRef.current, seg2FillRef.current, seg3FillRef.current],
        { scaleX: 0, transformOrigin: "left center" }
      );
      if (edgeSealRef.current) {
        gsap.set(edgeSealRef.current, { opacity: 0 });
      }
      if (videoFrameBoxRef.current) {
        gsap.set(videoFrameBoxRef.current, { borderRadius: "18px" });
      }

      /* Apertura sutil de escala (0.97 -> 1) en el bloque al abrir el estudio blanco */
      if (exitStageRef.current) {
        gsap.fromTo(
          exitStageRef.current,
          { y: 18 },
          {
            y: 0,
            ease: "power2.out",
            scrollTrigger: {
              trigger: sectionEl,
              start: "top 75%",
              end: "top top",
              scrub: isMobileOrTouch ? 0.18 : 0.24,
              invalidateOnRefresh: true,
            },
          }
        );
      }

      const tl = gsap.timeline({
        defaults: { ease: "none" },
        scrollTrigger: {
          trigger: sectionEl,
          start: "top top",
          end: "bottom bottom",
          pin: false,
          scrub: !isMobileOrTouch ? 0.28 : 0.22,
          invalidateOnRefresh: true,
          onUpdate: (self) => {
            updateStudioWhiteFlag(self.progress >= 0.22);
          },
          onScrubComplete: snapVideoToRestedFrame,
        },
      });

      /* Playhead normalizado 0 -> 1 sincronizado con la duración total del balón */
      tl.fromTo(
        playheadRef.current,
        { progress: 0 },
        { progress: 1, duration: 1, ease: "none" },
        0
      );

      /* Fusión óptica progresiva: el encuadre del césped se funde sin bordes al estudio #FFFFFF */
      if (edgeSealRef.current) {
        tl.fromTo(
          edgeSealRef.current,
          { opacity: 0 },
          { opacity: 1, duration: 0.12, ease: "power1.out" },
          0.16
        );
      }
      if (videoFrameBoxRef.current) {
        tl.to(
          videoFrameBoxRef.current,
          { borderRadius: "0px", duration: 0.1, ease: "power1.out" },
          0.18
        );
      }

      /* Indicador silencioso de 3 segmentos finos (2px: #070B14/15 -> #070B14) */
      tl.fromTo(
        seg1FillRef.current,
        { scaleX: 0 },
        { scaleX: 1, duration: 0.31, ease: "none" },
        0
      );
      tl.fromTo(
        seg2FillRef.current,
        { scaleX: 0 },
        { scaleX: 1, duration: 0.35, ease: "none" },
        0.31
      );
      tl.fromTo(
        seg3FillRef.current,
        { scaleX: 0 },
        { scaleX: 1, duration: 0.34, ease: "none" },
        0.66
      );

      /* =====================================================================
         COREOGRAFÍA DE 3 ACTOS CON EL MISMO RITMO SUAVE Y FLUIDO DE DUEÑOS:
         Mesetas cómodas de lectura + parpadeo elegante en opacidad (0 -> 0.45 -> 1)
         ===================================================================== */

      /* ACTO 1 (0.00 -> 0.29): meseta cómoda de lectura hasta 0.23 -> salida suave */
      tl.fromTo(
        act1Ref.current,
        { y: 0 },
        { y: -4, duration: 0.23, ease: "none" },
        0
      );
      tl.to(
        act1Ref.current,
        {
          autoAlpha: 0,
          y: -12,
          duration: 0.06,
          ease: "power2.inOut",
        },
        0.23
      );

      /* ACTO 2 (0.31 -> 0.64): emerge suave en dos tiempos + meseta cómoda -> salida suave */
      tl.to(
        act2Ref.current,
        {
          keyframes: [
            { autoAlpha: 0.45, y: 4, duration: 0.026, ease: "power2.out" },
            { autoAlpha: 1, y: 0, duration: 0.036, ease: "power2.inOut" },
          ],
        },
        0.31
      );
      tl.to(
        act2Ref.current,
        { y: -4, duration: 0.21, ease: "none" },
        0.372
      );
      tl.to(
        act2Ref.current,
        {
          autoAlpha: 0,
          y: -12,
          duration: 0.06,
          ease: "power2.inOut",
        },
        0.582
      );

      /* ACTO 3 (0.66 -> 1.00): emerge suave + amplia meseta de lectura y clic */
      tl.to(
        act3Ref.current,
        {
          keyframes: [
            { autoAlpha: 0.45, y: 4, duration: 0.026, ease: "power2.out" },
            { autoAlpha: 1, y: 0, duration: 0.038, ease: "power2.inOut" },
          ],
        },
        0.66
      );
      tl.to({}, { duration: 0.276 }, 0.724);

      /* Salida cinematográfica reversible mientras el velo #070B14 de Dueños asciende */
      if (exitStageRef.current) {
        gsap.fromTo(
          exitStageRef.current,
          { opacity: 1, y: 0 },
          {
            opacity: 0,
            y: !isMobileOrTouch ? -30 : -20,
            ease: "none",
            immediateRender: false,
            scrollTrigger: {
              trigger: sectionEl,
              start: "bottom bottom",
              end: !isMobileOrTouch ? "bottom 46%" : "bottom 50%",
              scrub: !isMobileOrTouch ? 0.26 : 0.2,
              invalidateOnRefresh: true,
            },
          }
        );
      }
    }, sectionEl);

    const onMobileTouchRelease = () => {
      mobileSettledExactApplied = false;
    };

    if (isMobileOrTouch) {
      window.addEventListener("touchend", onMobileTouchRelease, {
        passive: true,
      });
      window.addEventListener("touchcancel", onMobileTouchRelease, {
        passive: true,
      });
    }

    const syncBallFrame = () => {
      if (!isNearViewportRef.current) return;

      const v = videoRef.current;
      if (
        !v ||
        !canUseBallVideoRef.current ||
        videoFatalErrorRef.current ||
        v.readyState < 2 ||
        !Number.isFinite(v.duration) ||
        v.duration <= 0.1
      ) {
        return;
      }

      const maxTime = Math.max(0, v.duration - 0.04);
      const desiredTime = playheadRef.current.progress * maxTime;
      const delta = desiredTime - renderedTimeRef.current;

      /* Rama Móvil / Táctil 120Hz (throttle ~36ms, lerp 0.20 y settle exacto al soltar) */
      if (isMobileOrTouch) {
        const isMobileSettled = Math.abs(delta) <= 0.004;

        if (!isMobileSettled) {
          mobileSettledExactApplied = false;
          renderedTimeRef.current += delta * MOBILE_SCRUB_LERP;
        } else {
          renderedTimeRef.current = desiredTime;
        }

        const targetTime = Math.min(
          maxTime,
          Math.max(0, renderedTimeRef.current)
        );
        const nowMs = performance.now();
        const diffFromVideo = Math.abs(targetTime - v.currentTime);

        if (!isMobileSettled) {
          if (
            !v.seeking &&
            nowMs - lastMobileSeekMs >= MOBILE_SEEK_MIN_INTERVAL_MS &&
            diffFromVideo >= MOBILE_SEEK_MIN_DELTA_SEC
          ) {
            lastMobileSeekMs = nowMs;
            v.currentTime = targetTime;
          }
        } else if (!mobileSettledExactApplied && !v.seeking) {
          if (diffFromVideo >= 0.01) {
            lastMobileSeekMs = nowMs;
            v.currentTime = targetTime;
          }
          mobileSettledExactApplied = true;
        }
        return;
      }

      /* Rama Escritorio sincronizada con Lenis + GSAP ticker (throttle ~34ms / ~29Hz y settle limpio) */
      const isDesktopSettled = Math.abs(delta) <= 0.004;
      if (!isDesktopSettled) {
        desktopSettledExactApplied = false;
        renderedTimeRef.current += delta * DESKTOP_SCRUB_LERP;
      } else {
        renderedTimeRef.current = desiredTime;
      }

      const targetTime = Math.min(
        maxTime,
        Math.max(0, renderedTimeRef.current)
      );
      const nowMs = performance.now();
      const diffFromVideo = Math.abs(targetTime - v.currentTime);

      if (!isDesktopSettled) {
        if (
          v.readyState >= 2 &&
          !v.seeking &&
          nowMs - lastDesktopSeekMs >= DESKTOP_SEEK_MIN_INTERVAL_MS &&
          diffFromVideo >= DESKTOP_SEEK_MIN_DELTA_SEC
        ) {
          lastDesktopSeekMs = nowMs;
          v.currentTime = targetTime;
        }
      } else if (!desktopSettledExactApplied && v.readyState >= 2 && !v.seeking) {
        if (diffFromVideo >= 0.012) {
          lastDesktopSeekMs = nowMs;
          v.currentTime = targetTime;
        }
        desktopSettledExactApplied = true;
      }
    };

    gsap.ticker.add(syncBallFrame);

    return () => {
      window.removeEventListener("touchend", onMobileTouchRelease);
      window.removeEventListener("touchcancel", onMobileTouchRelease);
      gsap.ticker.remove(syncBallFrame);
      ctx.revert();
    };
  }, [reducedMotion, mediaVariant, isMobileOrTouch]);

  const activePoster =
    mediaVariant === "mobile"
      ? SITE_CONFIG.media.ballMobilePoster
      : SITE_CONFIG.media.ballDesktopPoster;

  const showPosterOverlay =
    !isVideoFrameReady || hasVideoError || saveDataMode;

  return (
    <section
      id="estudio-balon-4a2"
      ref={sectionRef}
      aria-label="Ingeniería de reserva en tiempo real"
      className={
        reducedMotion
          ? "ball-reduced-track relative z-20 w-full bg-[#FFFFFF] text-[#070B14] [overflow-x:clip]"
          : "ball-scroll-track relative z-20 w-full bg-[#FFFFFF] text-[#070B14] [overflow-x:clip]"
      }
    >
      {reducedMotion ? (
        /* Fallback accesible (prefers-reduced-motion): escena clara con balón + los 3 mensajes legibles */
        <div className="mx-auto flex w-full max-w-7xl flex-col gap-12 px-6 pt-24 pb-20 sm:px-8 lg:px-12 lg:pt-28 lg:pb-24">
          <div
            data-studio-white={isStudioWhite ? "true" : "false"}
            className="ball-studio-mask relative mx-auto aspect-video w-full max-w-[440px] overflow-hidden rounded-2xl bg-[#FFFFFF] md:max-w-[680px] lg:max-w-[880px]"
          >
            <video
              ref={videoRef}
              src={resolvedVideoSrc}
              muted
              playsInline
              preload={shouldLoadVideo ? "auto" : "none"}
              disablePictureInPicture
              disableRemotePlayback
              poster={!shouldLoadVideo ? activePoster : undefined}
              onError={handleVideoError}
              aria-hidden="true"
              {...INLINE_WEBVIEW_VIDEO_ATTRS}
              className={`pointer-events-none h-full w-full object-contain object-center select-none ${
                hasVideoError || saveDataMode ? "hidden" : ""
              }`}
            />
            <picture
              className={`pointer-events-none absolute inset-0 z-[1] block h-full w-full select-none transition-opacity duration-300 ${
                showPosterOverlay ? "opacity-100" : "opacity-0"
              }`}
            >
              <source
                media="(max-width: 767px)"
                srcSet={SITE_CONFIG.media.ballMobilePoster}
                type="image/webp"
              />
              <source
                media="(min-width: 768px)"
                srcSet={SITE_CONFIG.media.ballDesktopPoster}
                type="image/webp"
              />
              <img
                src={SITE_CONFIG.media.ballDesktopPoster}
                alt=""
                aria-hidden="true"
                loading="lazy"
                decoding="async"
                className="h-full w-full object-contain object-center"
              />
            </picture>
          </div>

          <div className="grid grid-cols-1 gap-10 md:grid-cols-3 lg:gap-12">
            <div className="flex flex-col items-start gap-3">
              <h2 className="font-display text-2xl leading-[1.06] font-bold tracking-[-0.03em] text-[#070B14] lg:text-[2rem]">
                Todas las canchas cerca de ti.
              </h2>
              <p className="text-[15px] leading-relaxed text-[#475569]">
                Filtra por grass 5v5, 7v7 u 11v11 con fotos y precios reales por
                hora.
              </p>
              <EditorialArrowLink
                href={SITE_CONFIG.routes.explore}
                label="Explorar canchas"
              />
            </div>

            <div className="flex flex-col items-start gap-3">
              <h2 className="font-display text-2xl leading-[1.06] font-bold tracking-[-0.03em] text-[#070B14] lg:text-[2rem]">
                Horarios actualizados en vivo.
              </h2>
              <p className="text-[15px] leading-relaxed text-[#475569]">
                Si aparece disponible, está libre. Sin doble reserva ni cruces.
              </p>
              <EditorialArrowLink
                href={SITE_CONFIG.routes.explore}
                label="Ver horarios libres"
              />
            </div>

            <div className="flex flex-col items-start gap-3">
              <h2 className="font-display text-2xl leading-[1.06] font-bold tracking-[-0.03em] text-[#070B14] lg:text-[2rem]">
                Paga, confirma y entra a jugar.
              </h2>
              <p className="text-[15px] leading-relaxed text-[#475569]">
                Recibes tu pase digital con código QR y la ubicación exacta del
                complejo en el mapa.
              </p>
              <div className="mt-1 flex flex-wrap items-center gap-4">
                <Link
                  href={SITE_CONFIG.routes.explore}
                  className="inline-flex min-h-[46px] items-center justify-center rounded-full bg-[#070B14] px-6 py-2.5 text-[14.5px] font-semibold text-white transition-colors duration-300 hover:bg-[#2563EB] focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-[#2563EB]"
                >
                  Reservar ahora
                </Link>
                <a
                  href={SITE_CONFIG.routes.ownersAnchor}
                  onClick={handleOwnersScroll}
                  className="text-sm font-medium text-[#475569] transition-colors duration-200 hover:text-[#070B14]"
                >
                  ¿Tienes un complejo? ↓
                </a>
              </div>
            </div>
          </div>
        </div>
      ) : (
        /* Escenario sticky nativo en GPU (100lvh + capa segura 100svh) sobre blanco puro #FFFFFF (cero velo gris) */
        <div className="hero-sticky-stage sticky top-0 w-full bg-[#FFFFFF] [overflow-x:clip]">
          <div
            ref={exitStageRef}
            className="hero-safe-ui-layer relative mx-auto flex w-full max-w-7xl flex-col justify-between px-6 pt-[4.5rem] pb-[max(1.75rem,env(safe-area-inset-bottom))] sm:px-8 sm:pt-[5.25rem] lg:px-12 lg:pt-[4.75rem] lg:pb-10 xl:pb-12"
          >
            {/* Zona protagonista del balón 16:9 íntegro 1:1 (cero recortes laterales, cero escala > 1, cero rectángulo gris) */}
            <div className="flex min-h-0 flex-1 items-center justify-center">
              <div
                ref={videoFrameBoxRef}
                data-studio-white={isStudioWhite ? "true" : "false"}
                className="ball-studio-mask relative mx-auto aspect-video w-full max-w-[440px] overflow-hidden bg-[#FFFFFF] sm:max-w-[520px] md:max-w-[620px] lg:h-[52svh] lg:max-h-[560px] lg:w-auto lg:max-w-[min(78vw,1000px)] xl:h-[56svh] xl:max-h-[600px] xl:max-w-[min(76vw,1060px)]"
              >
                <video
                  ref={videoRef}
                  src={resolvedVideoSrc}
                  muted
                  playsInline
                  preload={shouldLoadVideo ? "auto" : "none"}
                  disablePictureInPicture
                  disableRemotePlayback
                  poster={!shouldLoadVideo ? activePoster : undefined}
                  onError={handleVideoError}
                  aria-hidden="true"
                  {...INLINE_WEBVIEW_VIDEO_ATTRS}
                  className={`hero-gpu-video pointer-events-none h-full w-full object-contain object-center select-none ${
                    hasVideoError || saveDataMode ? "hidden" : ""
                  }`}
                />

                {/* Poster HD instantáneo (mismo fotograma 00:00.0) y respaldo ante Save-Data / sin red */}
                <picture
                  className={`pointer-events-none absolute inset-0 z-[1] block h-full w-full select-none transition-opacity duration-300 ${
                    showPosterOverlay ? "opacity-100" : "opacity-0"
                  }`}
                >
                  <source
                    media="(max-width: 767px)"
                    srcSet={SITE_CONFIG.media.ballMobilePoster}
                    type="image/webp"
                  />
                  <source
                    media="(min-width: 768px)"
                    srcSet={SITE_CONFIG.media.ballDesktopPoster}
                    type="image/webp"
                  />
                  <img
                    src={SITE_CONFIG.media.ballDesktopPoster}
                    alt=""
                    aria-hidden="true"
                    loading="lazy"
                    decoding="async"
                    className="h-full w-full object-contain object-center"
                  />
                </picture>

                {/* Sello perimetral #FFFFFF que funde bordes e iluminación de piso durante el desarme */}
                <div
                  ref={edgeSealRef}
                  aria-hidden="true"
                  className="ball-studio-edge-seal pointer-events-none absolute inset-0 z-[2] opacity-0"
                />
              </div>
            </div>

            {/* Bloque editorial aireado: centrado en móvil (< lg) y composición arquitectónica de 2 columnas en PC (lg:) */}
            <div className="relative z-10 mt-3 flex shrink-0 flex-col items-center lg:mt-4 lg:items-start">
              {/* Indicador sutil de avance de 3 segmentos finos (2px, #070B14/15 -> #070B14) */}
              <div
                aria-hidden="true"
                className="mb-4 flex items-center gap-2 lg:mb-5"
              >
                <span className="relative h-[2px] w-8 overflow-hidden rounded-full bg-[#070B14]/15 sm:w-10">
                  <span
                    ref={seg1FillRef}
                    className="block h-full w-full origin-left scale-x-0 bg-[#070B14]"
                  />
                </span>
                <span className="relative h-[2px] w-8 overflow-hidden rounded-full bg-[#070B14]/15 sm:w-10">
                  <span
                    ref={seg2FillRef}
                    className="block h-full w-full origin-left scale-x-0 bg-[#070B14]"
                  />
                </span>
                <span className="relative h-[2px] w-8 overflow-hidden rounded-full bg-[#070B14]/15 sm:w-10">
                  <span
                    ref={seg3FillRef}
                    className="block h-full w-full origin-left scale-x-0 bg-[#070B14]"
                  />
                </span>
              </div>

              {/* Pila de 3 actos sincronizados (solo un acto visible e interactivo a la vez) */}
              <div className="grid w-full grid-cols-1 grid-rows-1 items-end">
                {/* ACTO 1 */}
                <div
                  ref={act1Ref}
                  className="col-start-1 row-start-1 mx-auto flex w-full max-w-lg flex-col items-center text-center lg:mx-0 lg:max-w-none lg:flex-row lg:items-end lg:justify-between lg:gap-12 lg:text-left"
                >
                  <h2 className="font-display text-[clamp(1.75rem,6vw,2.35rem)] leading-[1.04] font-bold tracking-[-0.032em] text-[#070B14] lg:max-w-[33rem] lg:text-[2.75rem] lg:leading-[1.03] xl:max-w-[38rem] xl:text-[3.25rem]">
                    Todas las canchas cerca de ti.
                  </h2>

                  <div className="mt-2.5 flex max-w-md flex-col items-center gap-2.5 lg:mt-0 lg:max-w-[26rem] lg:items-start lg:pb-0.5">
                    <p className="text-[14.5px] leading-relaxed text-[#475569] sm:text-[15.5px] lg:text-[16px]">
                      Filtra por grass 5v5, 7v7 u 11v11 con fotos y precios
                      reales por hora.
                    </p>
                    <EditorialArrowLink
                      href={SITE_CONFIG.routes.explore}
                      label="Explorar canchas"
                    />
                  </div>
                </div>

                {/* ACTO 2 */}
                <div
                  ref={act2Ref}
                  className="invisible col-start-1 row-start-1 mx-auto flex w-full max-w-lg flex-col items-center text-center opacity-0 lg:mx-0 lg:max-w-none lg:flex-row lg:items-end lg:justify-between lg:gap-12 lg:text-left"
                >
                  <h2 className="font-display text-[clamp(1.75rem,6vw,2.35rem)] leading-[1.04] font-bold tracking-[-0.032em] text-[#070B14] lg:max-w-[33rem] lg:text-[2.75rem] lg:leading-[1.03] xl:max-w-[38rem] xl:text-[3.25rem]">
                    Horarios actualizados en vivo.
                  </h2>

                  <div className="mt-2.5 flex max-w-md flex-col items-center gap-2.5 lg:mt-0 lg:max-w-[26rem] lg:items-start lg:pb-0.5">
                    <p className="text-[14.5px] leading-relaxed text-[#475569] sm:text-[15.5px] lg:text-[16px]">
                      Si aparece disponible, está libre. Sin doble reserva ni
                      cruces.
                    </p>
                    <EditorialArrowLink
                      href={SITE_CONFIG.routes.explore}
                      label="Ver horarios libres"
                    />
                  </div>
                </div>

                {/* ACTO 3 */}
                <div
                  ref={act3Ref}
                  className="invisible col-start-1 row-start-1 mx-auto flex w-full max-w-lg flex-col items-center text-center opacity-0 lg:mx-0 lg:max-w-none lg:flex-row lg:items-end lg:justify-between lg:gap-12 lg:text-left"
                >
                  <h2 className="font-display text-[clamp(1.75rem,6vw,2.35rem)] leading-[1.04] font-bold tracking-[-0.032em] text-[#070B14] lg:max-w-[33rem] lg:text-[2.75rem] lg:leading-[1.03] xl:max-w-[38rem] xl:text-[3.25rem]">
                    Paga, confirma y entra a jugar.
                  </h2>

                  <div className="mt-2.5 flex max-w-md flex-col items-center gap-3.5 lg:mt-0 lg:max-w-[27rem] lg:items-start lg:pb-0.5">
                    <p className="text-[14.5px] leading-relaxed text-[#475569] sm:text-[15.5px] lg:text-[16px]">
                      Recibes tu pase digital con código QR y la ubicación
                      exacta del complejo en el mapa.
                    </p>

                    <div className="flex flex-wrap items-center justify-center gap-4 lg:justify-start">
                      <Link
                        href={SITE_CONFIG.routes.explore}
                        className="cta-pure-btn inline-flex min-h-[46px] items-center justify-center gap-2 rounded-full bg-[#070B14] px-6 py-2.5 text-[14.5px] font-semibold text-white transition-colors duration-300 hover:bg-[#2563EB] focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-[#2563EB] sm:min-h-[48px] sm:px-6.5 sm:text-[15px]"
                      >
                        <span>Reservar ahora</span>
                      </Link>

                      <a
                        href={SITE_CONFIG.routes.ownersAnchor}
                        onClick={handleOwnersScroll}
                        className="py-1.5 text-[13.5px] font-medium text-[#475569] transition-colors duration-200 hover:text-[#070B14] focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-[#070B14] sm:text-sm"
                      >
                        ¿Tienes un complejo? ↓
                      </a>
                    </div>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>
      )}
    </section>
  );
}
