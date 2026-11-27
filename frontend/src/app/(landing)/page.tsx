import { preload } from "react-dom";
import { LandingStage } from "@/landing/components/LandingStage";
import { SITE_CONFIG } from "@/shared/site-config";

export default function LandingHomePage() {
  /* Precarga adaptativa del poster WebP (-final) según viewport; desktop usa hero-cancha-desktop-final.mp4 (+faststart) */
  preload(SITE_CONFIG.media.heroMobilePoster, {
    as: "image",
    type: "image/webp",
    media: "(max-width: 767px)",
    fetchPriority: "high",
  });
  preload(SITE_CONFIG.media.heroDesktopPoster, {
    as: "image",
    type: "image/webp",
    media: "(min-width: 768px)",
    fetchPriority: "high",
  });

  return <LandingStage />;
}
