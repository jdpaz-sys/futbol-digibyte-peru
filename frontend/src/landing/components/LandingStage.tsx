"use client";

import { useCallback, useEffect, useRef } from "react";
import gsap from "gsap";
import { ScrollTrigger } from "gsap/ScrollTrigger";
import Lenis from "lenis";
import { BallDisassemblySection } from "@/landing/components/BallDisassemblySection";
import { ClosingLaunchSection } from "@/landing/components/ClosingLaunchSection";
import { HeroExitTransition } from "@/landing/components/HeroExitTransition";
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
    ScrollTrigger.config({ ignoreMobileResize: true });

    /* En pantallas táctiles (pointer: coarse) corre el scroll nativo a 120Hz sin Lenis */
    const isCoarsePointer = window.matchMedia("(pointer: coarse)").matches;
    if (isCoarsePointer) return;

    /* Calibrado sin doble arrastre (lerp 0.16) para acompañar el damping exponencial 120Hz del Hero */
    const lenis = new Lenis({
      autoRaf: false,
      smoothWheel: true,
      syncTouch: false,
      wheelMultiplier: 1.05,
      touchMultiplier: 1,
      lerp: 0.16,
    });
    lenisRef.current = lenis;
    lenis.scrollTo(window.scrollY, { immediate: true, force: true });

    lenis.on("scroll", ScrollTrigger.update);

    /* Prioridad alta (true): Lenis actualiza scroll antes que timelines y scrubbing en el mismo frame */
    const onGsapTick = (timeSeconds: number) => {
      lenis.raf(timeSeconds * 1000);
    };

    gsap.ticker.add(onGsapTick, false, true);
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

  const scrollToSectionSmooth = useCallback((targetSelector: string) => {
    const targetEl = document.querySelector<HTMLElement>(targetSelector);
    if (!targetEl) return;

    const prefersReducedMotion = window.matchMedia(
      "(prefers-reduced-motion: reduce)"
    ).matches;

    /* #duenos es un escenario sticky 100lvh a pantalla completa: offset 0 encaja al tope exacto */
    const navOffset = targetSelector === "#duenos" ? 0 : -84;

    if (lenisRef.current && !prefersReducedMotion) {
      lenisRef.current.scrollTo(targetEl, {
        offset: navOffset,
        duration: 1.15,
      });
      return;
    }

    const targetY = Math.max(
      0,
      window.scrollY + targetEl.getBoundingClientRect().top + navOffset
    );
    window.scrollTo({
      top: targetY,
      behavior: prefersReducedMotion ? "auto" : "smooth",
    });
  }, []);

  const scrollToTopSmooth = useCallback(() => {
    const prefersReducedMotion = window.matchMedia(
      "(prefers-reduced-motion: reduce)"
    ).matches;

    if (lenisRef.current && !prefersReducedMotion) {
      lenisRef.current.scrollTo(0, { duration: 1.2 });
      return;
    }

    window.scrollTo({
      top: targetYZero(),
      behavior: prefersReducedMotion ? "auto" : "smooth",
    });
  }, []);

  return (
    <div id="top">
      <LandingHeader onScrollToSection={scrollToSectionSmooth} />

      <main>
        <HeroSection onImmediateScroll={syncImmediateScroll} />

        <HeroExitTransition />

        <BallDisassemblySection onScrollToSection={scrollToSectionSmooth} />

        <ClosingLaunchSection
          onScrollToSection={scrollToSectionSmooth}
          onScrollToTop={scrollToTopSmooth}
        />
      </main>
    </div>
  );
}

function targetYZero(): number {
  return 0;
}
