"use client";

import { useEffect, useState } from "react";

interface HeaderScrollState {
  isScrolled: boolean;
  isOnLightSurface: boolean;
}

export function useScrollThreshold(
  thresholdPx = 24,
  lightSectionId = "estudio-balon-4a2"
): HeaderScrollState {
  const [state, setState] = useState<HeaderScrollState>({
    isScrolled: false,
    isOnLightSurface: false,
  });

  useEffect(() => {
    let ticking = false;

    const evaluate = () => {
      const nextScrolled = window.scrollY > thresholdPx;
      const lightEl = document.getElementById(lightSectionId);

      /* Entra a cristal claro en el instante exacto en que el borde blanco #FFFFFF cruza el navbar (44px)
         y vuelve a cristal oscuro cuando el velo ascendente #070B14 alcanza la franja superior */
      let nextLight = false;
      if (lightEl) {
        const rect = lightEl.getBoundingClientRect();
        const veilReachesHeaderPx = Math.round(window.innerHeight * 0.54);
        nextLight = rect.top <= 44 && rect.bottom > veilReachesHeaderPx;
      }

      setState((prev) =>
        prev.isScrolled === nextScrolled &&
        prev.isOnLightSurface === nextLight
          ? prev
          : { isScrolled: nextScrolled, isOnLightSurface: nextLight }
      );
      ticking = false;
    };

    const onScroll = () => {
      if (!ticking) {
        window.requestAnimationFrame(evaluate);
        ticking = true;
      }
    };

    evaluate();
    window.addEventListener("scroll", onScroll, { passive: true });
    window.addEventListener("resize", onScroll, { passive: true });

    return () => {
      window.removeEventListener("scroll", onScroll);
      window.removeEventListener("resize", onScroll);
    };
  }, [thresholdPx, lightSectionId]);

  return state;
}
