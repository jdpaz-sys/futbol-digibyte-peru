"use client";

import { useEffect, useState } from "react";

interface HeaderScrollState {
  isScrolled: boolean;
  isOnLightSurface: boolean;
}

export function useScrollThreshold(
  thresholdPx = 24,
  lightSectionId = "transicion-fase-4a2"
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
      const nextLight = lightEl
        ? lightEl.getBoundingClientRect().top <= 56
        : false;

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
