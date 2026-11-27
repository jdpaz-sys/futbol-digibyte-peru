"use client";

import { useEffect, useRef } from "react";
import gsap from "gsap";
import { ScrollTrigger } from "gsap/ScrollTrigger";

const EDITORIAL_STATEMENT =
  "Reservar una cancha nunca debió tomar tantos mensajes.";
const EDITORIAL_WORDS = EDITORIAL_STATEMENT.split(" ");

export function HeroExitTransition() {
  const sectionRef = useRef<HTMLElement | null>(null);
  const featherRef = useRef<HTMLDivElement | null>(null);
  const wordRefs = useRef<Array<HTMLSpanElement | null>>([]);

  useEffect(() => {
    const sectionEl = sectionRef.current;
    const featherEl = featherRef.current;
    if (!sectionEl || !featherEl) return;

    const prefersReducedMotion = window.matchMedia(
      "(prefers-reduced-motion: reduce)"
    ).matches;

    const words = wordRefs.current.filter(
      (node): node is HTMLSpanElement => node !== null
    );

    if (prefersReducedMotion) {
      gsap.set(featherEl, { clearProps: "transform" });
      gsap.set(words, { opacity: 1 });
      return;
    }

    gsap.registerPlugin(ScrollTrigger);
    ScrollTrigger.config({ ignoreMobileResize: true });

    const isCoarsePointer = window.matchMedia("(pointer: coarse)").matches;

    const ctx = gsap.context(() => {
      gsap.set(featherEl, { scaleY: 0.05 });
      gsap.set(words, { opacity: 0.18 });

      /* Velo atmosférico ascendente: crece suavemente al salir del Acto 3 sin corte horizontal */
      gsap.fromTo(
        featherEl,
        { scaleY: 0.05 },
        {
          scaleY: 1.06,
          ease: "power1.out",
          immediateRender: false,
          scrollTrigger: {
            trigger: sectionEl,
            start: "top bottom",
            end: "top 50%",
            scrub: isCoarsePointer ? 0.18 : 0.26,
            invalidateOnRefresh: true,
          },
        }
      );

      /* Iluminación progresiva palabra por palabra (slate tenue -> blanco-plata nítido) */
      if (words.length > 0) {
        const wordsTl = gsap.timeline({
          defaults: { ease: "none" },
          scrollTrigger: {
            trigger: sectionEl,
            start: "top 22%",
            end: "bottom bottom",
            pin: false,
            scrub: isCoarsePointer ? 0.2 : 0.28,
            invalidateOnRefresh: true,
          },
        });

        wordsTl.fromTo(
          words,
          { opacity: 0.18 },
          {
            opacity: 1,
            duration: 0.22,
            stagger: 0.082,
            ease: "power1.inOut",
          },
          0
        );

        /* Pausa editorial breve con la declaración 100% iluminada antes de salir hacia Fase 4A.2 */
        wordsTl.to({}, { duration: 0.21 }, 0.79);
      }
    }, sectionEl);

    return () => {
      ctx.revert();
    };
  }, []);

  return (
    <section
      ref={sectionRef}
      aria-label="Declaración editorial Fútbol Digibyte Perú"
      className="hero-exit-track relative z-20 w-full bg-[#070B14] text-white [overflow-x:clip]"
    >
      {/* Manto degradado ascendente (~40vh–44vh) que envuelve la cancha de abajo hacia arriba sin borde duro */}
      <div
        ref={featherRef}
        aria-hidden="true"
        className="hero-exit-feather pointer-events-none absolute inset-x-0 bottom-[calc(100%-2px)] z-10"
      />

      {/* Lienzo silencioso sticky 100lvh con centrado óptico en 100svh */}
      <div className="hero-sticky-stage sticky top-0 w-full bg-[#070B14] [overflow-x:clip]">
        <div className="hero-safe-ui-layer mx-auto flex w-full max-w-5xl items-center justify-center px-6 sm:px-10 lg:px-16">
          <p
            aria-label={EDITORIAL_STATEMENT}
            className="font-display max-w-[18.5ch] text-center text-[clamp(1.7rem,6.6vw,2.15rem)] leading-[1.12] font-semibold tracking-[-0.03em] text-balance sm:max-w-[20ch] sm:text-[2.25rem] md:text-[2.45rem] lg:text-[2.6rem] lg:leading-[1.12] lg:tracking-[-0.03em] xl:text-[3.1rem] 2xl:text-[3.4rem]"
          >
            {EDITORIAL_WORDS.map((word, index) => (
              <span key={`${word}-${index}`} aria-hidden="true">
                <span
                  ref={(el) => {
                    wordRefs.current[index] = el;
                  }}
                  className="hero-exit-word"
                >
                  {word}
                </span>
                {index < EDITORIAL_WORDS.length - 1 ? " " : ""}
              </span>
            ))}
          </p>
        </div>
      </div>
    </section>
  );
}
