import React from 'react';
import { QRCodeSVG } from 'qrcode.react';

export default function QRCode({ value, size = 196 }) {
  if (!value) {
    return (
      <div style={{ width: size, height: size, background: '#fff', borderRadius: 8 }} aria-label="QR code loading" />
    );
  }

  return (
    <div
      role="img"
      aria-label="QR code"
      style={{ width: size, height: size, display: 'block', borderRadius: 8, background: '#fff', overflow: 'hidden' }}
    >
      <QRCodeSVG
        value={value}
        size={size}
        level="M"
        marginSize={4}
        bgColor="#ffffff"
        fgColor="#111111"
      />
    </div>
  );
}
