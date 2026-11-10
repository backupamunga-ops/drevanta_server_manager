// QR rendering for the onboarding MFA step (node-testable, no browser
// APIs). The generated SVG MUST carry explicit width/height attributes:
// an <svg> with only a viewBox has no intrinsic size and collapses to
// 0x0 inside the fit-content .oqr container — in production the QR
// rendered as a blank white pill instead of a scannable code. QR_SIZE
// and the `.oqr svg` CSS rule in the onboarding page must stay in sync.
import QRCode from 'qrcode';

/** Pixel size (width and height) of the rendered QR code. */
export const QR_SIZE = 184;

/**
 * Render an otpauth:// URI as an inline SVG string with explicit
 * dimensions.
 *
 * @param {string} uri otpauth:// URI from the daemon's mfa/setup call
 * @param {number} [size] rendered width/height in pixels
 * @returns {Promise<string>} SVG markup with explicit width/height
 */
export async function qrSvg(uri, size = QR_SIZE) {
	return QRCode.toString(uri, {
		type: 'svg',
		width: size,
		margin: 1,
		color: { dark: '#0b1220', light: '#ffffff' }
	});
}
