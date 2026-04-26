type TurnBootstrapMode = 'off' | 'cloudflare' | 'integrated' | 'auto';

type UrlBucket = 'udp' | 'turns' | 'tcp' | 'other';

const integratedTurnBucketOrder: UrlBucket[] = ['udp', 'turns', 'tcp', 'other'];

const asUrlList = (urls: RTCIceServer['urls']): string[] => {
  if (Array.isArray(urls)) {
    return urls.filter((url): url is string => typeof url === 'string');
  }

  return typeof urls === 'string' ? [urls] : [];
};

const dedupeUrls = (urls: string[]) => {
  const seen = new Set<string>();
  const deduped: string[] = [];

  for (const rawUrl of urls) {
    const url = rawUrl.trim();
    if (!url || seen.has(url)) {
      continue;
    }

    seen.add(url);
    deduped.push(url);
  }

  return deduped;
};

const isTurnUrl = (url: string) => url.startsWith('turn:') || url.startsWith('turns:');

const integratedTurnUrlBucket = (url: string): UrlBucket => {
  if (url.startsWith('turns:')) {
    return 'turns';
  }

  const lowerUrl = url.toLowerCase();
  if (lowerUrl.includes('transport=udp')) {
    return 'udp';
  }
  if (lowerUrl.includes('transport=tcp')) {
    return 'tcp';
  }

  return 'other';
};

const normalizeIntegratedTurnUrls = (urls: string[]) => {
  const byBucket = new Map<UrlBucket, string>();

  for (const url of urls) {
    const bucket = integratedTurnUrlBucket(url);
    if (!byBucket.has(bucket)) {
      byBucket.set(bucket, url);
    }
  }

  return integratedTurnBucketOrder
    .map(bucket => byBucket.get(bucket))
    .filter((url): url is string => Boolean(url));
};

export function normalizeIceServersForBootstrap(
  iceServers: RTCIceServer[],
  mode?: string
): RTCIceServer[] {
  const normalizedMode = mode?.trim() as TurnBootstrapMode | undefined;

  return iceServers.reduce<RTCIceServer[]>((servers, server) => {
    const urls = dedupeUrls(asUrlList(server.urls));
    if (urls.length === 0) {
      return servers;
    }

    const turnUrls = urls.filter(isTurnUrl);
    const nonTurnUrls = urls.filter(url => !isTurnUrl(url));
    const normalizedTurnUrls =
      normalizedMode === 'integrated' ? normalizeIntegratedTurnUrls(turnUrls) : turnUrls;
    const normalizedUrls = [...nonTurnUrls, ...normalizedTurnUrls];

    if (normalizedUrls.length === 0) {
      return servers;
    }

    servers.push({
      ...server,
      urls: normalizedUrls,
    });

    return servers;
  }, []);
}
