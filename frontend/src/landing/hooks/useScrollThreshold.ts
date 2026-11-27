"use client";

import { useEffect, useState } from "react";
import gsap from "gsap";
import { ScrollTrigger } from "gsap/ScrollTrigger";

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
    gsap.registerPlugin(ScrollTrigger);

    let scrolledFlag = window.scrollY > thresholdPx;
    let lightFlag = false;

    const commitState = () => {
      setState((prev) =>
        prev.isScrolled === scrolledFlag &&
        prev.isOnLightSurface === lightFlag
          ? prev
          : { isScrolled: scrolledFlag, isOnLightSurface: lightFlag }
      );
    };

    const scrolledTrigger = ScrollTrigger.create({
      start: thresholdPx + 1,
      end: "max",
      onToggle: (self) => {
        if (scrolledFlag === self.isActive) return;
        scrolledFlag = self.isActive;
        commitState();
      },
      onRefresh: (self) => {
        scrolledFlag = self.isActive;
        commitState();
      },
    });

    /* Entra a cristal claro en el instante exacto en que el borde blanco #FFFFFF cruza el navbar (44px)
       y vuelve a cristal oscuro cuando el velo ascendente #070B14 alcanza la franja superior (54%).
       Cero lecturas de getBoundingClientRect() durante el scroll. */
    const lightEl = document.getElementById(lightSectionId);
    const lightTrigger = lightEl
      ? ScrollTrigger.create({
          trigger: lightEl,
          start: "top 44px",
          end: "bottom 54%",
          onToggle: (self) => {
            if (lightFlag === self.isActive) return;
            lightFlag = self.isActive;
            commitState();
          },
          onRefresh: (self) => {
            lightFlag = self.isActive;
            commitState();
          },
        })
      : null;

    scrolledFlag = scrolledTrigger.isActive;
    lightFlag = lightTrigger ? lightTrigger.isActive : false;
    commitState();

    return () => {
      scrolledTrigger.kill();
      lightTrigger?.kill();
    };
  }, [thresholdPx, lightSectionId]);

  return state;
}
