// Renders the favicon SVG into favicon.ico (16/32/48), apple-touch-icon, PWA icons and writes site.webmanifest (runs before astro build).
import { readFileSync, writeFileSync } from 'node:fs';
import { Resvg } from '@resvg/resvg-js';

const pub = new URL('../public/', import.meta.url);
const svg = readFileSync(new URL('mori-favicon.svg', pub), 'utf8');
const BG = '#F6F8F4';
const png = (src, size) => new Resvg(src, { fitTo: { mode: 'width', value: size } }).render().asPng();
/** Full-bleed square art: the rounded tile becomes a plain square (iOS and maskable icons round the corners themselves). */
const square = svg.replace('rx="14"', 'rx="0"');
/** Maskable icons keep their art inside the central 80% safe zone. */
const maskable = square.replace('viewBox="0 0 64 64"', 'viewBox="-8 -8 80 80"').replace('<rect width="64" height="64" rx="0"', '<rect x="-8" y="-8" width="80" height="80"');

function ico(images) {
  const head = Buffer.alloc(6 + images.length * 16);
  head.writeUInt16LE(1, 2);
  head.writeUInt16LE(images.length, 4);
  let offset = head.length;
  images.forEach(([size, data], i) => {
    const e = 6 + i * 16;
    head.writeUInt8(size, e);
    head.writeUInt8(size, e + 1);
    head.writeUInt16LE(1, e + 4);
    head.writeUInt16LE(32, e + 6);
    head.writeUInt32LE(data.length, e + 8);
    head.writeUInt32LE(offset, e + 12);
    offset += data.length;
  });
  return Buffer.concat([head, ...images.map(([, d]) => d)]);
}

writeFileSync(new URL('favicon.ico', pub), ico([16, 32, 48].map(s => [s, png(svg, s)])));
writeFileSync(new URL('apple-touch-icon.png', pub), png(square, 180));
writeFileSync(new URL('icon-192.png', pub), png(svg, 192));
writeFileSync(new URL('icon-512.png', pub), png(svg, 512));
writeFileSync(new URL('icon-maskable-512.png', pub), png(maskable, 512));
writeFileSync(new URL('site.webmanifest', pub), JSON.stringify({
  name: 'Mori 森记', short_name: 'Mori', description: 'Your Personal Digital Garden', start_url: '/', display: 'standalone',
  theme_color: '#2F4A3A', background_color: BG,
  icons: [
    { src: '/icon-192.png', sizes: '192x192', type: 'image/png' },
    { src: '/icon-512.png', sizes: '512x512', type: 'image/png' },
    { src: '/icon-maskable-512.png', sizes: '512x512', type: 'image/png', purpose: 'maskable' },
  ],
}, null, 2) + '\n');
