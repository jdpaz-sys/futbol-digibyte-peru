"use client";

import type { MouseEvent } from "react";
import { useEffect, useRef, useState } from "react";
import Image from "next/image";
import Link from "next/link";
import gsap from "gsap";
import { ScrollTrigger } from "gsap/ScrollTrigger";
import { SITE_CONFIG } from "@/shared/site-config";

interface ClosingLaunchSectionProps {
  onScrollToSection?: (targetSelector: string) => void;
  onScrollToTop?: () => void;
}

const OWNERS_STEPS = [
  {
    headline:
      "Controla tus canchas, adelantos y reservas desde un solo lugar.",
    support: "Deja atrás el desorden en cuadernos y chats de WhatsApp.",
  },
  {
    headline: "Cobra el adelanto antes del partido.",
    support: "Valida pagos con Yape o Plin y elimina las reservas fantasma.",
  },
  {
    headline: "Horarios siempre al día e ingreso verificado con QR.",
    support:
      "Evita cruces automáticamente y controla la puerta desde tu celular.",
  },
  {
    headline: "Publica tu complejo y recibe reservas confirmadas.",
    support: null,
  },
] as const;

const STEP1_WORDS = OWNERS_STEPS[0].headline.split(" ");
const STEP2_WORDS = OWNERS_STEPS[1].headline.split(" ");
const STEP3_WORDS = OWNERS_STEPS[2].headline.split(" ");
const STEP4_WORDS = OWNERS_STEPS[3].headline.split(" ");

const APPS_LINE1_WORDS = "Reserva hoy desde la web.".split(" ");
const APPS_LINE2_WORDS = "Muy pronto en Android y iOS.".split(" ");
const APPS_TOTAL_WORDS = APPS_LINE1_WORDS.length + APPS_LINE2_WORDS.length;

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

/* Ícono oficial de Google Play a 4 colores reales (#4285F4, #34A853, #FBBC05, #EA4335) */
function GooglePlayColorIcon() {
  return (
    <svg
      aria-hidden="true"
      viewBox="0 0 24 24"
      fill="none"
      className="h-8 w-8 shrink-0"
    >
      <path
        d="M3.35 2.85C3.12 3.16 3 3.63 3 4.22V19.78C3 20.37 3.12 20.84 3.35 21.15L13.15 12L3.35 2.85Z"
        fill="#4285F4"
      />
      <path
        d="M16.52 8.78L5.08 2.27C4.39 1.88 3.74 1.98 3.35 2.85L13.15 12L16.52 8.78Z"
        fill="#34A853"
      />
      <path
        d="M13.15 12L3.35 21.15C3.74 22.02 4.39 22.12 5.08 21.73L16.52 15.22L13.15 12Z"
        fill="#EA4335"
      />
      <path
        d="M20.18 10.86L16.52 8.78L13.15 12L16.52 15.22L20.18 13.14C21.14 12.6 21.14 11.4 20.18 10.86Z"
        fill="#FBBC05"
      />
    </svg>
  );
}

/* Ícono limpio de Apple en blanco/plata metálico */
function AppleSilverIcon() {
  return (
    <svg
      aria-hidden="true"
      viewBox="0 0 24 24"
      fill="none"
      className="h-8 w-8 shrink-0"
    >
      <defs>
        <linearGradient
          id="fdp-apple-silver"
          x1="12"
          y1="2"
          x2="12"
          y2="22.8"
          gradientUnits="userSpaceOnUse"
        >
          <stop offset="0%" stopColor="#FFFFFF" />
          <stop offset="55%" stopColor="#F1F5F9" />
          <stop offset="100%" stopColor="#CBD5E1" />
        </linearGradient>
      </defs>
      <path
        d="M16.365 2.004c-1.08.074-2.364.744-3.112 1.635-.669.793-1.244 2.054-1.082 3.255 1.206.091 2.444-.615 3.17-1.503.702-.858 1.227-2.094 1.024-3.387ZM19.93 17.07c-.504 1.153-.747 1.668-1.397 2.687-.908 1.422-2.186 3.195-3.772 3.21-1.409.015-1.772-.918-3.683-.905-1.911.011-2.31.922-3.72.907-1.586-.016-2.797-1.613-3.706-3.034-2.538-3.972-2.804-8.63-1.238-11.106 1.111-1.758 2.868-2.79 4.521-2.79 1.682 0 2.738.923 4.132.923 1.352 0 2.177-.925 4.118-.925 1.471 0 3.03.802 4.139 2.186-3.638 1.995-3.048 7.194.606 8.847Z"
        fill="url(#fdp-apple-silver)"
      />
    </svg>
  );
}

export function ClosingLaunchSection({
  onScrollToSection,
  onScrollToTop,
}: ClosingLaunchSectionProps) {
  const sectionRef = useRef<HTMLElement | null>(null);
  const topFeatherRef = useRef<HTMLDivElement | null>(null);
  const ownersTrackRef = useRef<HTMLDivElement | null>(null);
  const ownersExitStageRef = useRef<HTMLDivElement | null>(null);

  const chapterLineRef = useRef<HTMLSpanElement | null>(null);
  const chapterLabelRef = useRef<HTMLDivElement | null>(null);

  const step1WrapRef = useRef<HTMLDivElement | null>(null);
  const step2WrapRef = useRef<HTMLDivElement | null>(null);
  const step3WrapRef = useRef<HTMLDivElement | null>(null);
  const step4WrapRef = useRef<HTMLDivElement | null>(null);

  const step1SubRef = useRef<HTMLParagraphElement | null>(null);
  const step2SubRef = useRef<HTMLParagraphElement | null>(null);
  const step3SubRef = useRef<HTMLParagraphElement | null>(null);
  const step4CtaRef = useRef<HTMLDivElement | null>(null);

  const step1WordsRef = useRef<Array<HTMLSpanElement | null>>([]);
  const step2WordsRef = useRef<Array<HTMLSpanElement | null>>([]);
  const step3WordsRef = useRef<Array<HTMLSpanElement | null>>([]);
  const step4WordsRef = useRef<Array<HTMLSpanElement | null>>([]);

  const pipeWrapRef = useRef<HTMLDivElement | null>(null);
  const pipeNodesTrackRef = useRef<HTMLDivElement | null>(null);
  const pipeBallRef = useRef<HTMLSpanElement | null>(null);
  const pipeNodesRef = useRef<Array<HTMLSpanElement | null>>([]);

  const appsSectionRef = useRef<HTMLDivElement | null>(null);
  const appsLineRef = useRef<HTMLSpanElement | null>(null);
  const appsTitleWrapRef = useRef<HTMLHeadingElement | null>(null);
  const appsWordsRef = useRef<Array<HTMLSpanElement | null>>([]);
  const appsSubRef = useRef<HTMLParagraphElement | null>(null);
  const appsBadgesRef = useRef<HTMLDivElement | null>(null);
  const appsCtaRef = useRef<HTMLDivElement | null>(null);

  const [reducedMotion, setReducedMotion] = useState(false);
  const [hasSweptEyebrow, setHasSweptEyebrow] = useState(false);

  useEffect(() => {
    const motionQuery = window.matchMedia("(prefers-reduced-motion: reduce)");
    const syncMotion = () => setReducedMotion(motionQuery.matches);
    syncMotion();
    motionQuery.addEventListener("change", syncMotion);
    return () => motionQuery.removeEventListener("change", syncMotion);
  }, []);

  useEffect(() => {
    const sectionEl = sectionRef.current;
    const ownersTrackEl = ownersTrackRef.current;
    if (!sectionEl || reducedMotion) return;

    gsap.registerPlugin(ScrollTrigger);
    ScrollTrigger.config({ ignoreMobileResize: true });

    const isCoarsePointer = window.matchMedia("(pointer: coarse)").matches;

    let targetNodeU = 0;
    let renderedNodeU = 0;
    let maxPipeTravelPx = 215;
    let isOwnersNearRef = false;

    const measurePipeTravel = () => {
      const nodesTrackEl = pipeNodesTrackRef.current;
      if (!nodesTrackEl) return;
      maxPipeTravelPx = Math.max(120, nodesTrackEl.offsetHeight - 5);
    };

    const renderPipeState = (u: number) => {
      const ballEl = pipeBallRef.current;
      const clampedU = Math.min(3, Math.max(0, u));

      if (ballEl) {
        const yPx = (clampedU / 3) * maxPipeTravelPx;
        const frac = clampedU - Math.floor(clampedU);
        const transitPulse = Math.sin(Math.PI * frac);

        const scaleY = 1 + 0.72 * transitPulse;
        const scaleX = 1 - 0.08 * transitPulse;
        ballEl.style.transform = `translate3d(0, ${yPx.toFixed(2)}px, 0) scale(${scaleX.toFixed(3)}, ${scaleY.toFixed(3)})`;
      }

      for (let i = 0; i < 4; i++) {
        const nodeEl = pipeNodesRef.current[i];
        if (!nodeEl) continue;
        const dist = Math.abs(clampedU - i);
        const proximity =
          dist < 0.42 ? Math.cos((dist / 0.42) * (Math.PI / 2)) : 0;
        const scale = 1 + 0.32 * proximity;
        const opacity = 0.24 + 0.76 * proximity;
        nodeEl.style.transform = `scale(${scale.toFixed(3)})`;
        nodeEl.style.opacity = opacity.toFixed(3);
      }
    };

    const tickPipe = () => {
      if (!isOwnersNearRef) return;
      const delta = targetNodeU - renderedNodeU;
      if (Math.abs(delta) <= 0.0015) {
        if (renderedNodeU !== targetNodeU) {
          renderedNodeU = targetNodeU;
          renderPipeState(renderedNodeU);
        }
        return;
      }
      renderedNodeU += delta * 0.24;
      renderPipeState(renderedNodeU);
    };

    measurePipeTravel();
    renderPipeState(0);

    const ctx = gsap.context(() => {
      /* 1. Velo atmosférico ascendente en #070B14 al salir de BallDisassemblySection (#FFFFFF) */
      if (topFeatherRef.current) {
        gsap.set(topFeatherRef.current, { scaleY: 0.05 });
        gsap.fromTo(
          topFeatherRef.current,
          { scaleY: 0.05 },
          {
            scaleY: 1.06,
            ease: "power1.out",
            immediateRender: false,
            scrollTrigger: {
              trigger: sectionEl,
              start: "top bottom",
              end: "top 46%",
              scrub: isCoarsePointer ? 0.18 : 0.26,
              invalidateOnRefresh: true,
            },
          }
        );
      }

      if (ownersTrackEl) {
        const w1 = step1WordsRef.current.filter(
          (n): n is HTMLSpanElement => n !== null
        );
        const w2 = step2WordsRef.current.filter(
          (n): n is HTMLSpanElement => n !== null
        );
        const w3 = step3WordsRef.current.filter(
          (n): n is HTMLSpanElement => n !== null
        );
        const w4 = step4WordsRef.current.filter(
          (n): n is HTMLSpanElement => n !== null
        );

        ScrollTrigger.create({
          trigger: ownersTrackEl,
          start: "top 120%",
          end: "bottom -20%",
          onToggle: (self) => {
            isOwnersNearRef = self.isActive;
          },
          onRefresh: (self) => {
            isOwnersNearRef = self.isActive;
            measurePipeTravel();
            renderPipeState(renderedNodeU);
          },
        });

        /* Apertura arquitectónica del rótulo superior */
        if (chapterLineRef.current) {
          gsap.set(chapterLineRef.current, { scaleX: 0 });
          gsap.fromTo(
            chapterLineRef.current,
            { scaleX: 0 },
            {
              scaleX: 1,
              ease: "power2.out",
              scrollTrigger: {
                trigger: ownersTrackEl,
                start: "top 68%",
                end: "top 18%",
                scrub: isCoarsePointer ? 0.18 : 0.24,
                invalidateOnRefresh: true,
              },
            }
          );
        }

        if (chapterLabelRef.current) {
          gsap.set(chapterLabelRef.current, { opacity: 0, y: 8 });
          gsap.fromTo(
            chapterLabelRef.current,
            { opacity: 0, y: 8 },
            {
              opacity: 1,
              y: 0,
              ease: "power2.out",
              scrollTrigger: {
                trigger: ownersTrackEl,
                start: "top 68%",
                end: "top 28%",
                scrub: isCoarsePointer ? 0.18 : 0.24,
                invalidateOnRefresh: true,
              },
            }
          );
        }

        /* Recorrido horizontal fino por las letras (izq -> der) la 1ra vez que la sección queda en foco */
        ScrollTrigger.create({
          trigger: ownersTrackEl,
          start: "top 22%",
          once: true,
          onEnter: () => {
            setHasSweptEyebrow(true);
          },
        });

        gsap.set(step1WrapRef.current, { autoAlpha: 1, y: 10 });
        gsap.set(
          [step2WrapRef.current, step3WrapRef.current, step4WrapRef.current],
          { autoAlpha: 0, y: 12 }
        );
        gsap.set([w1, w2, w3, w4], { opacity: 0.15 });
        gsap.set(
          [step1SubRef.current, step2SubRef.current, step3SubRef.current],
          { opacity: 0, y: 8 }
        );
        gsap.set(step4CtaRef.current, { opacity: 0, y: 12, scale: 0.97 });

        /* Timeline continuo de los 4 pasos B2B (start: "top 22%" -> "bottom bottom") */
        const stepsTl = gsap.timeline({
          defaults: { ease: "none" },
          scrollTrigger: {
            trigger: ownersTrackEl,
            start: "top 22%",
            end: "bottom bottom",
            pin: false,
            scrub: isCoarsePointer ? 0.2 : 0.28,
            invalidateOnRefresh: true,
            onUpdate: (self) => {
              targetNodeU = self.progress * 3;
            },
          },
        });

        /* PASO 1 (0.00 -> 0.245) */
        stepsTl.fromTo(
          step1WrapRef.current,
          { y: 10 },
          { y: -6, duration: 0.21, ease: "none" },
          0
        );
        if (w1.length > 0) {
          const stagger1 =
            w1.length > 1 ? Number((0.125 / (w1.length - 1)).toFixed(4)) : 0;
          stepsTl.fromTo(
            w1,
            { opacity: 0.15 },
            {
              opacity: 1,
              duration: 0.045,
              stagger: stagger1,
              ease: "power1.inOut",
            },
            0
          );
        }
        stepsTl.fromTo(
          step1SubRef.current,
          { opacity: 0, y: 8 },
          { opacity: 1, y: 0, duration: 0.06, ease: "power2.out" },
          0.11
        );
        stepsTl.to(
          step1WrapRef.current,
          {
            autoAlpha: 0,
            y: -16,
            duration: 0.042,
            ease: "power2.inOut",
          },
          0.205
        );

        /* PASO 2 (0.255 -> 0.495) */
        stepsTl.to(
          step2WrapRef.current,
          {
            keyframes: [
              { autoAlpha: 0.45, duration: 0.02, ease: "power2.out" },
              { autoAlpha: 1, duration: 0.028, ease: "power2.inOut" },
            ],
          },
          0.255
        );
        stepsTl.fromTo(
          step2WrapRef.current,
          { y: 12 },
          { y: -6, duration: 0.2, ease: "none" },
          0.255
        );
        if (w2.length > 0) {
          const stagger2 =
            w2.length > 1 ? Number((0.11 / (w2.length - 1)).toFixed(4)) : 0;
          stepsTl.fromTo(
            w2,
            { opacity: 0.15 },
            {
              opacity: 1,
              duration: 0.045,
              stagger: stagger2,
              ease: "power1.inOut",
            },
            0.258
          );
        }
        stepsTl.fromTo(
          step2SubRef.current,
          { opacity: 0, y: 8 },
          { opacity: 1, y: 0, duration: 0.055, ease: "power2.out" },
          0.35
        );
        stepsTl.to(
          step2WrapRef.current,
          {
            autoAlpha: 0,
            y: -16,
            duration: 0.042,
            ease: "power2.inOut",
          },
          0.455
        );

        /* PASO 3 (0.505 -> 0.745) */
        stepsTl.to(
          step3WrapRef.current,
          {
            keyframes: [
              { autoAlpha: 0.45, duration: 0.02, ease: "power2.out" },
              { autoAlpha: 1, duration: 0.028, ease: "power2.inOut" },
            ],
          },
          0.505
        );
        stepsTl.fromTo(
          step3WrapRef.current,
          { y: 12 },
          { y: -6, duration: 0.2, ease: "none" },
          0.505
        );
        if (w3.length > 0) {
          const stagger3 =
            w3.length > 1 ? Number((0.115 / (w3.length - 1)).toFixed(4)) : 0;
          stepsTl.fromTo(
            w3,
            { opacity: 0.15 },
            {
              opacity: 1,
              duration: 0.045,
              stagger: stagger3,
              ease: "power1.inOut",
            },
            0.508
          );
        }
        stepsTl.fromTo(
          step3SubRef.current,
          { opacity: 0, y: 8 },
          { opacity: 1, y: 0, duration: 0.055, ease: "power2.out" },
          0.605
        );
        stepsTl.to(
          step3WrapRef.current,
          {
            autoAlpha: 0,
            y: -16,
            duration: 0.042,
            ease: "power2.inOut",
          },
          0.705
        );

        /* PASO 4 (0.755 -> 1.000) */
        stepsTl.to(
          step4WrapRef.current,
          {
            keyframes: [
              { autoAlpha: 0.45, duration: 0.02, ease: "power2.out" },
              { autoAlpha: 1, duration: 0.03, ease: "power2.inOut" },
            ],
          },
          0.755
        );
        stepsTl.fromTo(
          step4WrapRef.current,
          { y: 14 },
          { y: 0, duration: 0.245, ease: "power1.out" },
          0.755
        );
        if (w4.length > 0) {
          const stagger4 =
            w4.length > 1 ? Number((0.115 / (w4.length - 1)).toFixed(4)) : 0;
          stepsTl.fromTo(
            w4,
            { opacity: 0.15 },
            {
              opacity: 1,
              duration: 0.048,
              stagger: stagger4,
              ease: "power1.inOut",
            },
            0.758
          );
        }
        stepsTl.fromTo(
          step4CtaRef.current,
          { opacity: 0, y: 12, scale: 0.97 },
          {
            opacity: 1,
            y: 0,
            scale: 1,
            duration: 0.075,
            ease: "power2.out",
          },
          0.855
        );
        stepsTl.to({}, { duration: 0.07 }, 0.93);

        /* =====================================================================
           TRANSICIÓN CINEMATOGRÁFICA DE SALIDA DE #duenos HACIA EL CIERRE DE APPS:
           En vez de subir duro y chocar contra el Navbar, el escenario completo
           retrocede en profundidad (scale 0.95 + autoAlpha -> 0) y la línea
           vertical se repliega limpia antes de tocar el borde superior (100% reversible)
           ===================================================================== */
        if (ownersExitStageRef.current) {
          gsap.set(ownersExitStageRef.current, {
            autoAlpha: 1,
            scale: 1,
            y: 0,
          });
          gsap.fromTo(
            ownersExitStageRef.current,
            { autoAlpha: 1, scale: 1, y: 0 },
            {
              autoAlpha: 0,
              scale: 0.945,
              y: 28,
              ease: "power1.inOut",
              immediateRender: false,
              scrollTrigger: {
                trigger: ownersTrackEl,
                start: "bottom bottom",
                end: "bottom 62%",
                scrub: isCoarsePointer ? 0.18 : 0.26,
                invalidateOnRefresh: true,
              },
            }
          );
        }

        if (pipeWrapRef.current) {
          gsap.set(pipeWrapRef.current, { autoAlpha: 1, scaleY: 1 });
          gsap.fromTo(
            pipeWrapRef.current,
            { autoAlpha: 1, scaleY: 1 },
            {
              autoAlpha: 0,
              scaleY: 0.35,
              ease: "power2.in",
              immediateRender: false,
              scrollTrigger: {
                trigger: ownersTrackEl,
                start: "bottom bottom",
                end: "bottom 68%",
                scrub: isCoarsePointer ? 0.16 : 0.22,
                invalidateOnRefresh: true,
              },
            }
          );
        }
      }

      /* =====================================================================
         ENTRADA CINEMATOGRÁFICA AL SCROLL DEL BLOQUE DE APPS (REVERSIBLE CON SCRUB):
         Emerge de la oscuridad mientras #duenos se disuelve en profundidad,
         encendiendo también sus palabras de silueta tenue a blanco-plata nítido
         ===================================================================== */
      const appsEl = appsSectionRef.current;
      if (appsEl) {
        const appWords = appsWordsRef.current.filter(
          (n): n is HTMLSpanElement => n !== null
        );

        gsap.set(appsLineRef.current, { scaleX: 0 });
        gsap.set(appsTitleWrapRef.current, { scale: 0.96, y: 24 });
        if (appWords.length > 0) {
          gsap.set(appWords, { opacity: 0.15 });
        }
        gsap.set(appsSubRef.current, { opacity: 0, y: 18 });
        gsap.set(appsBadgesRef.current, { opacity: 0, y: 22 });
        gsap.set(appsCtaRef.current, { opacity: 0, y: 20, scale: 0.97 });

        const appsTl = gsap.timeline({
          defaults: { ease: "none" },
          scrollTrigger: {
            trigger: appsEl,
            start: "top 86%",
            end: "top 24%",
            scrub: isCoarsePointer ? 0.2 : 0.28,
            invalidateOnRefresh: true,
          },
        });

        appsTl.to(
          appsLineRef.current,
          { scaleX: 1, duration: 0.28, ease: "power2.out" },
          0
        );
        appsTl.to(
          appsTitleWrapRef.current,
          { scale: 1, y: 0, duration: 0.55, ease: "power2.out" },
          0.06
        );

        if (appWords.length > 0) {
          const appStagger =
            appWords.length > 1
              ? Number((0.46 / (appWords.length - 1)).toFixed(4))
              : 0;
          appsTl.fromTo(
            appWords,
            { opacity: 0.15 },
            {
              opacity: 1,
              duration: 0.18,
              stagger: appStagger,
              ease: "power1.inOut",
            },
            0.1
          );
        }

        appsTl.to(
          appsSubRef.current,
          { opacity: 1, y: 0, duration: 0.3, ease: "power2.out" },
          0.46
        );
        appsTl.to(
          appsBadgesRef.current,
          { opacity: 1, y: 0, duration: 0.32, ease: "power2.out" },
          0.58
        );
        appsTl.to(
          appsCtaRef.current,
          { opacity: 1, y: 0, scale: 1, duration: 0.3, ease: "power2.out" },
          0.7
        );
      }
    }, sectionEl);

    const onResize = () => {
      measurePipeTravel();
      renderPipeState(renderedNodeU);
    };

    window.addEventListener("resize", onResize, { passive: true });
    gsap.ticker.add(tickPipe);

    return () => {
      window.removeEventListener("resize", onResize);
      gsap.ticker.remove(tickPipe);
      ctx.revert();
    };
  }, [reducedMotion]);

  const handleOwnersScroll = (event: MouseEvent<HTMLAnchorElement>) => {
    if (!onScrollToSection) return;
    const target = document.querySelector(SITE_CONFIG.routes.ownersAnchor);
    if (!target) return;
    event.preventDefault();
    onScrollToSection(SITE_CONFIG.routes.ownersAnchor);
  };

  const handleBackToTop = (event: MouseEvent<HTMLAnchorElement>) => {
    event.preventDefault();
    if (onScrollToTop) {
      onScrollToTop();
      return;
    }
    window.scrollTo({ top: 0, behavior: "smooth" });
  };

  return (
    <section
      ref={sectionRef}
      aria-label="Sección para dueños de complejos deportivos y descarga de aplicación"
      className="relative z-30 w-full bg-[#070B14] text-white [overflow-x:clip]"
    >
      {/* 1. VELO ATMOSFÉRICO OSCURO ASCENDENTE (#FFFFFF -> #070B14) */}
      {!reducedMotion ? (
        <div
          ref={topFeatherRef}
          aria-hidden="true"
          className="hero-exit-feather pointer-events-none absolute inset-x-0 bottom-[calc(100%-2px)] z-10"
        />
      ) : null}

      {/* 2. SECCIÓN DUEÑOS DE COMPLEJOS DEPORTIVOS (id="duenos") */}
      <div
        id="duenos"
        ref={ownersTrackRef}
        className="owners-b2b-track relative z-10 w-full bg-[#070B14] [overflow-x:clip]"
      >
        {reducedMotion ? (
          <div className="mx-auto flex w-full max-w-3xl flex-col items-center gap-16 px-8 py-24 text-center sm:px-10 lg:max-w-[56rem] lg:py-32">
            <div className="flex flex-col items-center">
              <span aria-hidden="true" className="h-px w-16 bg-white/25" />
              <p className="tabular-mono mt-5 text-[13px] font-medium tracking-[0.2em] text-balance text-white/50 uppercase sm:text-[14px]">
                ¿ADMINISTRAS UN COMPLEJO DEPORTIVO?
              </p>
            </div>

            {OWNERS_STEPS.map((step, index) => (
              <div key={step.headline} className="flex flex-col items-center">
                <h2 className="closing-silver-title font-display text-[clamp(2.05rem,6.6vw,3.35rem)] leading-[1.06] font-semibold tracking-[-0.035em] text-balance lg:text-[3.6rem]">
                  {step.headline}
                </h2>
                {step.support ? (
                  <p className="mt-5 max-w-2xl text-[17px] leading-relaxed text-white/65 sm:mt-6 sm:text-[18.5px] lg:text-[19.5px]">
                    {step.support}
                  </p>
                ) : null}
                {index === 3 ? (
                  <div className="mt-10 flex flex-col items-center sm:mt-12">
                    <Link
                      href={SITE_CONFIG.routes.login}
                      className="cta-pure-btn inline-flex min-h-[56px] items-center justify-center gap-3 rounded-full bg-white px-10 py-4 text-[16.5px] font-semibold tracking-[-0.01em] text-[#070B14] shadow-[0_0_40px_rgba(255,255,255,0.16)] transition-all duration-300 hover:bg-[#F1F5F9] hover:shadow-[0_0_52px_rgba(255,255,255,0.26)] focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-white sm:text-[17.5px]"
                    >
                      <span>Registrar mi complejo</span>
                      <CtaArrowIcon />
                    </Link>
                    <p className="mt-4 text-[13.5px] text-white/50 sm:text-[14.5px]">
                      Activación directa con el equipo de Fútbol Digibyte Perú
                    </p>
                  </div>
                ) : null}
              </div>
            ))}
          </div>
        ) : (
          <div className="hero-sticky-stage sticky top-0 w-full bg-[#070B14] [overflow-x:clip]">
            <div className="hero-safe-ui-layer relative mx-auto flex w-full max-w-6xl flex-col items-center justify-center px-8 py-16 sm:px-12 md:px-16">
              {/* Contenedor con disolución cinematográfica en profundidad al salir hacia Apps */}
              <div
                ref={ownersExitStageRef}
                className="flex w-full flex-col items-center justify-center origin-center will-change-[transform,opacity]"
              >
                {/* APERTURA DE CAPÍTULO B2B — 100% SIMÉTRICA AL CENTRO EXACTO */}
                <div className="mb-8 flex w-full flex-col items-center text-center sm:mb-10 lg:mb-11">
                  <span
                    ref={chapterLineRef}
                    aria-hidden="true"
                    className="mx-auto block h-px w-16 origin-center bg-white/25"
                  />
                  <div
                    ref={chapterLabelRef}
                    className="mt-4 flex justify-center sm:mt-5"
                  >
                    {/* pl-[0.2em] compensa el letter-spacing final para centrado óptico milimétrico;
                        el texto base permanece en white/45 y solo pasa el barrido fino por las letras */}
                    <p className="tabular-mono relative inline-block pl-[0.2em] text-center text-[13px] font-medium tracking-[0.2em] text-white/45 uppercase sm:text-[14px] lg:text-[14.5px]">
                      <span>¿ADMINISTRAS UN COMPLEJO DEPORTIVO?</span>
                      <span
                        aria-hidden="true"
                        data-sweep={hasSweptEyebrow ? "true" : "false"}
                        className="owners-eyebrow-beam pointer-events-none absolute inset-0 pl-[0.2em] select-none"
                      >
                        ¿ADMINISTRAS UN COMPLEJO DEPORTIVO?
                      </span>
                    </p>
                  </div>
                </div>

                {/* 4 PASOS ALINEADOS AL MISMO EJE CENTRAL Y TOPE SUPERIOR (items-start evita saltos verticales) */}
                <div className="mx-auto grid min-h-[210px] w-full max-w-3xl grid-cols-1 grid-rows-1 items-start justify-items-center text-center sm:min-h-[230px] lg:min-h-[250px] lg:max-w-[56rem]">
                  {/* PASO 1 */}
                  <div
                    ref={step1WrapRef}
                    className="col-start-1 row-start-1 flex w-full flex-col items-center text-center"
                  >
                    <h2
                      aria-label={OWNERS_STEPS[0].headline}
                      className="font-display text-[clamp(2.05rem,6.6vw,2.75rem)] leading-[1.06] font-semibold tracking-[-0.035em] text-balance sm:text-[2.9rem] md:text-[3.2rem] lg:text-[3.55rem] xl:text-[3.85rem]"
                    >
                      {STEP1_WORDS.map((word, idx) => (
                        <span key={`s1-${word}-${idx}`} aria-hidden="true">
                          <span
                            ref={(el) => {
                              step1WordsRef.current[idx] = el;
                            }}
                            className="owners-step-word"
                          >
                            {word}
                          </span>
                          {idx < STEP1_WORDS.length - 1 ? " " : ""}
                        </span>
                      ))}
                    </h2>
                    <p
                      ref={step1SubRef}
                      className="mt-5 max-w-xl text-[17px] leading-relaxed text-white/65 sm:mt-6 sm:text-[18.5px] lg:max-w-2xl lg:text-[19.5px]"
                    >
                      {OWNERS_STEPS[0].support}
                    </p>
                  </div>

                  {/* PASO 2 */}
                  <div
                    ref={step2WrapRef}
                    className="invisible col-start-1 row-start-1 flex w-full flex-col items-center text-center opacity-0"
                  >
                    <h2
                      aria-label={OWNERS_STEPS[1].headline}
                      className="font-display text-[clamp(2.1rem,6.8vw,2.85rem)] leading-[1.05] font-semibold tracking-[-0.036em] text-balance sm:text-[3rem] md:text-[3.3rem] lg:text-[3.65rem] xl:text-[3.95rem]"
                    >
                      {STEP2_WORDS.map((word, idx) => (
                        <span key={`s2-${word}-${idx}`} aria-hidden="true">
                          <span
                            ref={(el) => {
                              step2WordsRef.current[idx] = el;
                            }}
                            className="owners-step-word"
                          >
                            {word}
                          </span>
                          {idx < STEP2_WORDS.length - 1 ? " " : ""}
                        </span>
                      ))}
                    </h2>
                    <p
                      ref={step2SubRef}
                      className="mt-5 max-w-xl text-[17px] leading-relaxed text-white/65 sm:mt-6 sm:text-[18.5px] lg:max-w-2xl lg:text-[19.5px]"
                    >
                      {OWNERS_STEPS[1].support}
                    </p>
                  </div>

                  {/* PASO 3 */}
                  <div
                    ref={step3WrapRef}
                    className="invisible col-start-1 row-start-1 flex w-full flex-col items-center text-center opacity-0"
                  >
                    <h2
                      aria-label={OWNERS_STEPS[2].headline}
                      className="font-display text-[clamp(2.05rem,6.6vw,2.8rem)] leading-[1.06] font-semibold tracking-[-0.035em] text-balance sm:text-[2.95rem] md:text-[3.25rem] lg:text-[3.6rem] xl:text-[3.9rem]"
                    >
                      {STEP3_WORDS.map((word, idx) => (
                        <span key={`s3-${word}-${idx}`} aria-hidden="true">
                          <span
                            ref={(el) => {
                              step3WordsRef.current[idx] = el;
                            }}
                            className="owners-step-word"
                          >
                            {word}
                          </span>
                          {idx < STEP3_WORDS.length - 1 ? " " : ""}
                        </span>
                      ))}
                    </h2>
                    <p
                      ref={step3SubRef}
                      className="mt-5 max-w-xl text-[17px] leading-relaxed text-white/65 sm:mt-6 sm:text-[18.5px] lg:max-w-2xl lg:text-[19.5px]"
                    >
                      {OWNERS_STEPS[2].support}
                    </p>
                  </div>

                  {/* PASO 4 */}
                  <div
                    ref={step4WrapRef}
                    className="invisible col-start-1 row-start-1 flex w-full flex-col items-center text-center opacity-0"
                  >
                    <h2
                      aria-label={OWNERS_STEPS[3].headline}
                      className="font-display text-[clamp(2.1rem,6.8vw,2.85rem)] leading-[1.05] font-semibold tracking-[-0.036em] text-balance sm:text-[3rem] md:text-[3.3rem] lg:text-[3.65rem] xl:text-[3.95rem]"
                    >
                      {STEP4_WORDS.map((word, idx) => (
                        <span key={`s4-${word}-${idx}`} aria-hidden="true">
                          <span
                            ref={(el) => {
                              step4WordsRef.current[idx] = el;
                            }}
                            className="owners-step-word"
                          >
                            {word}
                          </span>
                          {idx < STEP4_WORDS.length - 1 ? " " : ""}
                        </span>
                      ))}
                    </h2>

                    <div
                      ref={step4CtaRef}
                      className="mt-10 flex flex-col items-center sm:mt-12"
                    >
                      <Link
                        href={SITE_CONFIG.routes.login}
                        className="cta-pure-btn inline-flex min-h-[56px] items-center justify-center gap-3 rounded-full bg-white px-10 py-4 text-[16.5px] font-semibold tracking-[-0.01em] text-[#070B14] shadow-[0_0_40px_rgba(255,255,255,0.16)] transition-all duration-300 hover:scale-[1.02] hover:bg-[#F1F5F9] hover:shadow-[0_0_52px_rgba(255,255,255,0.28)] active:scale-[0.99] focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-white sm:text-[17.5px]"
                      >
                        <span>Registrar mi complejo</span>
                        <CtaArrowIcon />
                      </Link>

                      <p className="mt-4 text-[13.5px] text-white/50 sm:text-[14.5px]">
                        Activación directa con el equipo de Fútbol Digibyte Perú
                      </p>
                    </div>
                  </div>
                </div>
              </div>

              {/* LÍNEA VERTICAL MINIMALISTA SIN BORDE EXTERIOR (SE REPLIEGA LIMPIA AL SALIR) */}
              <div
                ref={pipeWrapRef}
                aria-hidden="true"
                className="pointer-events-none absolute top-1/2 right-3.5 -translate-y-1/2 origin-center will-change-[transform,opacity] md:right-6 lg:right-8"
              >
                <div className="owners-minimal-line relative h-[200px] w-[1.5px] rounded-full sm:h-[235px] lg:h-[260px]">
                  <div
                    ref={pipeNodesTrackRef}
                    className="absolute inset-y-[10px] left-1/2 flex -translate-x-1/2 flex-col items-center justify-between"
                  >
                    {[0, 1, 2, 3].map((nodeIdx) => (
                      <span
                        key={nodeIdx}
                        ref={(el) => {
                          pipeNodesRef.current[nodeIdx] = el;
                        }}
                        style={{
                          opacity: nodeIdx === 0 ? 1 : 0.24,
                          transform: nodeIdx === 0 ? "scale(1.32)" : "scale(1)",
                        }}
                        className="h-[5px] w-[5px] shrink-0 rounded-full bg-white shadow-[0_0_8px_rgba(255,255,255,0.6)] will-change-[transform,opacity]"
                      />
                    ))}

                    <span
                      ref={pipeBallRef}
                      style={{
                        transform: "translate3d(0, 0px, 0) scale(1, 1)",
                      }}
                      className="absolute -top-[1.5px] left-1/2 z-10 -ml-[4px] h-[8px] w-[8px] origin-center rounded-full bg-white shadow-[0_0_12px_rgba(255,255,255,0.85)] will-change-transform"
                    />
                  </div>
                </div>
              </div>
            </div>
          </div>
        )}
      </div>

      {/* 3. REVELACIÓN CINEMATOGRÁFICA DEL CIERRE DE APPS (EMPALME EN PROFUNDIDAD SIN RAYA DURA) */}
      <div className="relative z-20 mx-auto -mt-[14vh] w-full max-w-7xl px-5 pb-12 sm:-mt-[18vh] sm:px-8 lg:px-12">
        <div
          ref={appsSectionRef}
          className="flex min-h-[82svh] flex-col items-center justify-center py-20 sm:py-24 lg:py-32"
        >
          <div className="mx-auto flex max-w-3xl flex-col items-center text-center">
            <div className="flex flex-col items-center">
              <span
                ref={appsLineRef}
                aria-hidden="true"
                className="block h-px w-16 origin-center bg-white/25"
              />

              <h2
                ref={appsTitleWrapRef}
                aria-label="Reserva hoy desde la web. Muy pronto en Android y iOS."
                className="font-display mt-7 text-[clamp(2.05rem,6vw,3.75rem)] leading-[1.05] font-bold tracking-[-0.035em] text-balance sm:mt-8"
              >
                <span className="block">
                  {APPS_LINE1_WORDS.map((word, idx) => (
                    <span key={`al1-${word}-${idx}`} aria-hidden="true">
                      <span
                        ref={(el) => {
                          appsWordsRef.current[idx] = el;
                        }}
                        className="owners-step-word"
                      >
                        {word}
                      </span>
                      {idx < APPS_LINE1_WORDS.length - 1 ? " " : ""}
                    </span>
                  ))}
                </span>
                <span className="mt-1 block">
                  {APPS_LINE2_WORDS.map((word, idx) => {
                    const globalIdx = APPS_LINE1_WORDS.length + idx;
                    return (
                      <span key={`al2-${word}-${idx}`} aria-hidden="true">
                        <span
                          ref={(el) => {
                            if (globalIdx < APPS_TOTAL_WORDS) {
                              appsWordsRef.current[globalIdx] = el;
                            }
                          }}
                          className="owners-step-word"
                        >
                          {word}
                        </span>
                        {idx < APPS_LINE2_WORDS.length - 1 ? " " : ""}
                      </span>
                    );
                  })}
                </span>
              </h2>

              <p
                ref={appsSubRef}
                className="mt-5 max-w-xl text-[16px] leading-relaxed text-white/65 sm:mt-6 sm:text-[18px]"
              >
                Usa la app web ahora mismo desde cualquier celular sin instalar
                nada. Muy pronto disponible en tu tienda de apps favorita.
              </p>
            </div>

            <div className="mt-10 flex w-full flex-col items-center sm:mt-11">
              {/* Badges sobrios de cristal neutro */}
              <div
                ref={appsBadgesRef}
                className="flex w-full flex-col items-center"
              >
                <div
                  role="group"
                  aria-label="Tiendas de aplicaciones próximamente"
                  className="mx-auto flex w-full max-w-xl flex-col items-stretch justify-center gap-3.5 sm:flex-row sm:items-center sm:gap-4"
                >
                  {/* Google Play (ícono oficial 4 colores) */}
                  <div
                    aria-disabled="true"
                    title="App móvil en preparación · Usa la App Web hoy mismo"
                    className="flex flex-1 items-center justify-between gap-4 rounded-2xl border border-white/12 bg-white/[0.03] px-5 py-4 text-left transition-colors duration-250 hover:border-white/22 hover:bg-white/[0.055]"
                  >
                    <div className="flex min-w-0 items-center gap-3.5">
                      <GooglePlayColorIcon />
                      <div className="flex min-w-0 flex-col">
                        <span className="tabular-mono text-[10px] font-medium tracking-[0.14em] whitespace-nowrap text-white/50 uppercase">
                          PRÓXIMAMENTE EN
                        </span>
                        <span className="font-display mt-0.5 text-lg leading-tight font-semibold whitespace-nowrap text-white">
                          Google Play
                        </span>
                      </div>
                    </div>
                    <span className="shrink-0 rounded-full border border-white/10 bg-white/[0.04] px-2.5 py-0.5 text-[11px] font-medium text-white/60">
                      Android
                    </span>
                  </div>

                  {/* App Store (ícono Apple blanco/plata) */}
                  <div
                    aria-disabled="true"
                    title="App móvil en preparación · Usa la App Web hoy mismo"
                    className="flex flex-1 items-center justify-between gap-4 rounded-2xl border border-white/12 bg-white/[0.03] px-5 py-4 text-left transition-colors duration-250 hover:border-white/22 hover:bg-white/[0.055]"
                  >
                    <div className="flex min-w-0 items-center gap-3.5">
                      <AppleSilverIcon />
                      <div className="flex min-w-0 flex-col">
                        <span className="tabular-mono text-[10px] font-medium tracking-[0.14em] whitespace-nowrap text-white/50 uppercase">
                          PRÓXIMAMENTE EN
                        </span>
                        <span className="font-display mt-0.5 text-lg leading-tight font-semibold whitespace-nowrap text-white">
                          App Store
                        </span>
                      </div>
                    </div>
                    <span className="shrink-0 rounded-full border border-white/10 bg-white/[0.04] px-2.5 py-0.5 text-[11px] font-medium text-white/60">
                      iOS
                    </span>
                  </div>
                </div>

                <p className="mt-4 text-center text-xs text-white/45 sm:text-[13px]">
                  App móvil en preparación · Usa la App Web hoy mismo
                </p>
              </div>

              {/* CTA principal protagonista en blanco puro */}
              <div
                ref={appsCtaRef}
                className="mt-9 flex w-full flex-col items-center gap-3 sm:w-auto"
              >
                <Link
                  href={SITE_CONFIG.routes.explore}
                  className="cta-pure-btn inline-flex min-h-[54px] w-full items-center justify-center gap-2.5 rounded-full bg-[#FFFFFF] px-9 py-4 text-[15.5px] font-semibold text-[#070B14] shadow-[0_0_36px_rgba(255,255,255,0.14)] transition-all duration-300 hover:scale-[1.02] hover:bg-[#F1F5F9] hover:shadow-[0_0_48px_rgba(255,255,255,0.24)] active:scale-[0.99] focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-white sm:w-auto sm:min-w-[260px] sm:text-[16px]"
                >
                  <span>Abrir App Web ahora</span>
                  <CtaArrowIcon />
                </Link>

                <p className="text-xs text-white/45 sm:text-[13px]">
                  Funciona en Android, iOS y PC sin descargas
                </p>
              </div>
            </div>
          </div>
        </div>

        {/* FOOTER MINIMALISTA AWWWARDS (#070B14) */}
        <footer className="border-t border-white/10 pt-8 pb-[max(1.5rem,env(safe-area-inset-bottom))]">
          <div className="flex flex-col justify-between gap-6 sm:flex-row sm:items-center">
            <div className="flex items-center gap-3">
              <Image
                src={SITE_CONFIG.media.logo}
                alt={SITE_CONFIG.name}
                width={30}
                height={30}
                className="h-[30px] w-[30px] shrink-0 object-contain"
              />
              <div className="flex flex-wrap items-center gap-x-2.5 gap-y-0.5 text-sm">
                <span className="font-display font-semibold tracking-[-0.02em] text-white">
                  {SITE_CONFIG.name}
                </span>
                <span aria-hidden="true" className="text-white/25">
                  ·
                </span>
                <span className="text-white/60">Huacho, Perú</span>
              </div>
            </div>

            <nav
              aria-label="Enlaces de pie de página"
              className="flex flex-wrap items-center gap-x-6 gap-y-2 text-[13.5px] text-white/65 sm:text-sm"
            >
              <Link
                href={SITE_CONFIG.routes.explore}
                className="transition-colors duration-200 hover:text-white focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-white"
              >
                Explorar canchas
              </Link>
              <Link
                href={SITE_CONFIG.routes.login}
                className="transition-colors duration-200 hover:text-white focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-white"
              >
                Iniciar sesión
              </Link>
              <a
                href={SITE_CONFIG.routes.ownersAnchor}
                onClick={handleOwnersScroll}
                className="transition-colors duration-200 hover:text-white focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-white"
              >
                Soy dueño
              </a>
              <a
                href="#top"
                onClick={handleBackToTop}
                className="font-medium text-white/85 transition-colors duration-200 hover:text-white focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-white"
              >
                Volver arriba ↑
              </a>
            </nav>
          </div>

          <div className="mt-6 text-xs text-white/45">
            © 2026 Fútbol Digibyte Perú. Todos los derechos reservados.
          </div>
        </footer>
      </div>
    </section>
  );
}
