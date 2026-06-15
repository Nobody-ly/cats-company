import React, { act } from 'react';
import { createRoot } from 'react-dom/client';
import QRCode from './qr-code';

describe('QRCode', () => {
  let container;
  let root;

  beforeEach(() => {
    global.IS_REACT_ACT_ENVIRONMENT = true;
    container = document.createElement('div');
    document.body.appendChild(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => {
      root.unmount();
    });
    container.remove();
  });

  it('renders a library-generated QR image', async () => {
    await act(async () => {
      root.render(<QRCode value="https://app.example.test/mobile-upload/test" />);
    });

    const image = container.querySelector('[role="img"][aria-label="QR code"]');
    const svg = image.querySelector('svg');
    expect(svg).not.toBeNull();
    expect(svg.getAttribute('height')).toBe('196');
    expect(svg.getAttribute('width')).toBe('196');
  });
});
