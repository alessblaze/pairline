import React, { useEffect, useRef } from 'react';
import Hls from 'hls.js';

interface HLSVideoPlayerProps extends React.VideoHTMLAttributes<HTMLVideoElement> {
  src: string;
}

export const HLSVideoPlayer: React.FC<HLSVideoPlayerProps> = ({
  src,
  autoPlay = false,
  muted = false,
  playsInline = true,
  ...videoProps
}) => {
  const videoRef = useRef<HTMLVideoElement>(null);

  useEffect(() => {
    const video = videoRef.current;
    if (!video) return;

    let hls: Hls | null = null;
    let destroyed = false;

    const isNativeHls =
      video.canPlayType('application/vnd.apple.mpegurl') !== '';

    const shouldMute = Boolean(autoPlay || muted);

    video.autoplay = autoPlay;
    video.playsInline = playsInline;

    if (shouldMute) {
      video.muted = true;
      video.defaultMuted = true;
      video.setAttribute('muted', '');
    } else {
      video.muted = false;
      video.defaultMuted = false;
      video.removeAttribute('muted');
    }

    if (playsInline) {
      video.setAttribute('playsinline', '');
      video.setAttribute('webkit-playsinline', '');
    } else {
      video.removeAttribute('playsinline');
      video.removeAttribute('webkit-playsinline');
    }

    const tryPlay = async () => {
      if (!autoPlay || destroyed) return;
      try {
        await video.play();
      } catch {
        // Browser autoplay policy may still block playback.
      }
    };

    const handleVisibilityChange = () => {
      if (document.visibilityState === 'visible' && autoPlay) {
        // Restart video from the beginning when browser comes back to foreground
        video.currentTime = 0;
        void tryPlay();
      }
    };

    document.addEventListener('visibilitychange', handleVisibilityChange);

    if (isNativeHls) {
      const handleLoadedMetadata = () => {
        void tryPlay();
      };

      video.addEventListener('loadedmetadata', handleLoadedMetadata);
      video.src = src;
      video.load();

      return () => {
        destroyed = true;
        document.removeEventListener('visibilitychange', handleVisibilityChange);
        video.removeEventListener('loadedmetadata', handleLoadedMetadata);
        video.pause();
        video.removeAttribute('src');
        video.load();
      };
    }

    if (Hls.isSupported()) {
      hls = new Hls({
        enableWorker: true,
        lowLatencyMode: true,
      });

      const onMediaAttached = () => {
        if (!destroyed) {
          hls?.loadSource(src);
        }
      };

      const onManifestParsed = () => {
        void tryPlay();
      };

      const onError = (_event: string, data: any) => {
        if (!hls || !data?.fatal) return;

        switch (data.type) {
          case Hls.ErrorTypes.NETWORK_ERROR:
            hls.startLoad();
            break;
          case Hls.ErrorTypes.MEDIA_ERROR:
            hls.recoverMediaError();
            break;
          default:
            hls.destroy();
            hls = null;
            break;
        }
      };

      hls.on(Hls.Events.MEDIA_ATTACHED, onMediaAttached);
      hls.on(Hls.Events.MANIFEST_PARSED, onManifestParsed);
      hls.on(Hls.Events.ERROR, onError);
      hls.attachMedia(video);

      return () => {
        destroyed = true;
        document.removeEventListener('visibilitychange', handleVisibilityChange);

        if (hls) {
          hls.off(Hls.Events.MEDIA_ATTACHED, onMediaAttached);
          hls.off(Hls.Events.MANIFEST_PARSED, onManifestParsed);
          hls.off(Hls.Events.ERROR, onError);
          hls.stopLoad();
          hls.detachMedia();
          hls.destroy();
        }

        video.pause();
        video.removeAttribute('src');
        video.load();
      };
    }

    video.src = src;
    video.load();
    void tryPlay();

    return () => {
      destroyed = true;
      document.removeEventListener('visibilitychange', handleVisibilityChange);
      video.pause();
      video.removeAttribute('src');
      video.load();
    };
  }, [src, autoPlay, muted, playsInline]);

  return (
    <video
      ref={videoRef}
      {...videoProps}
      playsInline={playsInline}
      autoPlay={autoPlay}
      muted={Boolean(autoPlay || muted)}
    />
  );
};