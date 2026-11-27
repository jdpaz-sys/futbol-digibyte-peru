"use client";

import { useEffect, useRef } from "react";
import gsap from "gsap";
import { ScrollTrigger } from "gsap/ScrollTrigger";

const HERO_EXIT_STATEMENT =
  "Deja de escribir a cinco canchas para encontrar una libre.";

export interface EditorialWordBridgeProps {
  statement: string;
  ariaLabel: string;
  trackClassName?: string;
  wordsStartTrigger?: string;
  withTopFeather?: boolean;
  withExitDrift?: boolean;
}

export function EditorialWordBridge({
  statement,
  ariaLabel,
  trackClassName = "hero-exit-track",
  wordsStartTrigger = "top 24%",
  withTopFeather = false,
  withExitDrift = true,
}: EditorialWordBridgeProps) {
  const sectionRef = useRef<HTMLElement | null>(null);
  const featherRef = useRef<HTMLDivElement | null>(null);
  const phraseWrapRef = useRef<HTMLParagraphElement | null>(null);
  const wordRefs = useRef<Array<HTMLSpanElement | null>>([]);

  const wordsList = statement.split(" ");

  useEffect(() => {
    const sectionEl = sectionRef.current;
    if (!sectionEl) return;

    const featherEl = featherRef.current;
    const phraseWrapEl = phraseWrapRef.current;

    const prefersReducedMotion = window.matchMedia(
      "(prefers-reduced-motion: reduce)"
    ).matches;

    const wordNodes = wordRefs.current.filter(
      (node): node is HTMLSpanElement => node !== null
    );

    if (prefersReducedMotion) {
      if (featherEl) gsap.set(featherEl, { clearProps: "transform" });
      if (phraseWrapEl)
        gsap.set(phraseWrapEl, { clearProps: "transform,opacity,visibility" });
      gsap.set(wordNodes, { opacity: 1 });
      return;
    }

    gsap.registerPlugin(ScrollTrigger);
    ScrollTrigger.config({ ignoreMobileResize: true });

    const isCoarsePointer = window.matchMedia("(pointer: coarse)").matches;

    const ctx = gsap.context(() => {
      if (withTopFeather && featherEl) {
        gsap.set(featherEl, { scaleY: 0.05 });

        /* Velo atmosférico ascendente en #070B14: absorbe la escena previa sin corte horizontal */
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
              end: "top 46%",
              scrub: isCoarsePointer ? 0.18 : 0.26,
              invalidateOnRefresh: true,
            },
          }
        );
      }

      /* Iluminación progresiva ligera y serena (mismo ritmo fluido de Dueños) */
      if (wordNodes.length > 0) {
        gsap.set(wordNodes, { opacity: 0.16 });
        if (phraseWrapEl) {
          gsap.set(phraseWrapEl, { autoAlpha: 1, y: 10, scale: 1 });
        }

        const wordStagger =
          wordNodes.length > 1
            ? Number(((0.7 - 0.2) / (wordNodes.length - 1)).toFixed(4))
            : 0;

        const wordsTl = gsap.timeline({
          defaults: { ease: "none" },
          scrollTrigger: {
            trigger: sectionEl,
            start: wordsStartTrigger,
            end: "bottom bottom",
            pin: false,
            scrub: isCoarsePointer ? 0.2 : 0.28,
            invalidateOnRefresh: true,
          },
        });

        if (phraseWrapEl) {
          wordsTl.fromTo(
            phraseWrapEl,
            { y: 10 },
            { y: -4, duration: 0.85, ease: "none" },
            0
          );
        }

        wordsTl.fromTo(
          wordNodes,
          { opacity: 0.16 },
          {
            opacity: 1,
            duration: 0.2,
            stagger: wordStagger,
            ease: "power1.inOut",
          },
          0
        );

        /* Meseta cómoda de lectura con todas las palabras iluminadas (0.70 -> 1.00) */
        wordsTl.to({}, { duration: 0.3 }, 0.7);
      }

      /* Desvanecimiento limpio ANTES de acercarse al navbar (jamás queda texto cortado arriba) */
      if (withExitDrift && phraseWrapEl) {
        gsap.fromTo(
          phraseWrapEl,
          { autoAlpha: 1, scale: 1, y: -4 },
          {
            autoAlpha: 0,
            scale: 0.96,
            y: 22,
            ease: "power1.inOut",
            immediateRender: false,
            scrollTrigger: {
              trigger: sectionEl,
              start: "bottom bottom",
              end: "bottom 64%",
              scrub: isCoarsePointer ? 0.16 : 0.24,
              invalidateOnRefresh: true,
            },
          }
        );
      }
    }, sectionEl);

    return () => {
      ctx.revert();
    };
  }, [statement, wordsStartTrigger, withTopFeather, withExitDrift]);

  return (
    <div
      ref={(el) => {
        sectionRef.current = el;
      }}
      role="region"
      aria-label={ariaLabel}
      className={`${trackClassName} relative z-20 w-full bg-[#070B14] text-white [overflow-x:clip]`}
    >
      {withTopFeather ? (
        <div
          ref={featherRef}
          aria-hidden="true"
          className="hero-exit-feather pointer-events-none absolute inset-x-0 bottom-[calc(100%-2px)] z-10"
        />
      ) : null}

      <div className="hero-sticky-stage sticky top-0 w-full bg-[#070B14] [overflow-x:clip]">
        <div className="hero-safe-ui-layer mx-auto flex w-full max-w-5xl items-center justify-center px-6 sm:px-10 lg:px-16">
          <p
            ref={phraseWrapRef}
            aria-label={statement}
            className="font-display max-w-[20ch] text-center text-[clamp(1.75rem,6.6vw,2.2rem)] leading-[1.12] font-semibold tracking-[-0.032em] text-balance sm:max-w-[21ch] sm:text-[2.3rem] md:text-[2.5rem] lg:text-[2.7rem] lg:leading-[1.1] xl:text-[3.2rem] 2xl:text-[3.5rem]"
          >
            {wordsList.map((word, index) => (
              <span key={`${word}-${index}`} aria-hidden="true">
                <span
                  ref={(el) => {
                    wordRefs.current[index] = el;
                  }}
                  className="hero-exit-word"
                >
                  {word}
                </span>
                {index < wordsList.length - 1 ? " " : ""}
              </span>
            ))}
          </p>
        </div>
      </div>
    </div>
  );
}

export function HeroExitTransition() {
  return (
    <EditorialWordBridge
      statement={HERO_EXIT_STATEMENT}
      ariaLabel="Declaración editorial Fútbol Digibyte Perú"
      trackClassName="hero-exit-track"
      wordsStartTrigger="top 24%"
      withTopFeather
      withExitDrift
    />
  );
}
