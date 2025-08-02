/* eslint-disable no-undef */
const path = require('path');

/**
 * Sanitize filename to prevent path traversal
 * @param {string} filename - The filename to sanitize
 * @returns {string} - Sanitized filename
 */
function sanitizeFilename(filename) {
  if (!filename) return 'screenshot.png';

  // Remove any path components (both forward and back slashes)
  const basename = filename.split(/[\\/]/).pop() || 'screenshot.png';

  // Allow only safe characters: alphanumeric, dots, hyphens, underscores
  const sanitized = basename.replace(/[^a-zA-Z0-9._-]/g, '_');

  // Ensure it has a .png extension
  if (!sanitized.endsWith('.png')) {
    return sanitized + '.png';
  }

  return sanitized;
}

/**
 * Validate and resolve safe output path
 * @param {string} outputDir - The base output directory
 * @param {string} filename - The filename
 * @returns {string} - Full safe path
 */
function getSafeOutputPath(outputDir, filename) {
  // Resolve the output directory to an absolute path
  const resolvedDir = path.resolve(__dirname, outputDir);

  // Sanitize the filename
  const safeFilename = sanitizeFilename(filename);

  // Join safely
  return path.join(resolvedDir, safeFilename);
}

module.exports = {
  sanitizeFilename,
  getSafeOutputPath,
};
