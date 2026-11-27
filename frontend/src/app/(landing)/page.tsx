import { preload } from "react-dom";
import { LandingStage } from "@/landing/components/LandingStage";
import { SITE_CONFIG } from "@/shared/site-config";

export default function LandingHomePage() {
  /* Precarga adaptativa exclusiva del poster WebP HD -v2 según viewport para pintado instantáneo en 4G */
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
