"use client";

import { useCallback, useEffect, useRef } from "react";
import gsap from "gsap";
import { ScrollTrigger } from "gsap/ScrollTrigger";
import Lenis from "lenis";
import { HeroSection } from "@/landing/components/HeroSection";
import { LandingHeader } from "@/landing/components/LandingHeader";

export function LandingStage() {
  const lenisRef = useRef<Lenis | null>(null);

  /* Control manual de scrollRestoration + Lenis sincronizado con gsap.ticker */
  useEffect(() => {
    if ("scrollRestoration" in window.history) {
      window.history.scrollRestoration = "manual";
    }

    const prefersReducedMotion = window.matchMedia(
      "(prefers-reduced-motion: reduce)"
    ).matches;
    if (prefersReducedMotion) return;

    gsap.registerPlugin(ScrollTrigger);

    const lenis = new Lenis({
      smoothWheel: true,
      syncTouch: false,
      touchMultiplier: 1,
      lerp: 0.1,
    });
    lenisRef.current = lenis;
    lenis.scrollTo(window.scrollY, { immediate: true, force: true });

    lenis.on("scroll", ScrollTrigger.update);

    const onGsapTick = (timeSeconds: number) => {
      lenis.raf(timeSeconds * 1000);
    };

    gsap.ticker.add(onGsapTick);
    gsap.ticker.lagSmoothing(0);

    return () => {
      gsap.ticker.remove(onGsapTick);
      lenis.destroy();
      lenisRef.current = null;
    };
  }, []);

  const syncImmediateScroll = useCallback((targetY: number) => {
    window.scrollTo(0, targetY);
    if (lenisRef.current) {
      lenisRef.current.scrollTo(targetY, { immediate: true, force: true });
    }
  }, []);

  return (
    <>
      <LandingHeader />

      <main>
        <HeroSection onImmediateScroll={syncImmediateScroll} />

        {/* Sección blanca (#FFFFFF) lista para conectar con Fase 4A.2 y validar recarga inferior */}
        <section
          id="transicion-fase-4a2"
          aria-label="Inicio de sección blanca Fase 4A.2"
          className="relative flex min-h-[85vh] items-center border-t border-[#E2E8F0] bg-[#FFFFFF] text-[#070B14]"
        >
          <div className="mx-auto flex w-full max-w-7xl flex-col items-start justify-between gap-3 px-6 py-20 sm:flex-row sm:items-center sm:px-8 lg:px-12">
            <span className="tabular-mono text-xs font-medium tracking-[0.18em] text-[#475569] uppercase">
              SIGUIENTE ESCENA · FASE 4A.2
            </span>
            <p className="text-sm text-[#64748B]">
              Transición a estudio blanco puro{" "}
              <span className="tabular-mono text-[#070B14]">#FFFFFF</span> lista
              para la secuencia interactiva del balón.
            </p>
          </div>
        </section>
      </main>
    </>
  );
}
