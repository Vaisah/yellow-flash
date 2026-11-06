/** Turns engine error text into an explanation a user can act on. */
export interface Explanation {
  title: string
  hint: string
}

const RULES: { test: RegExp; title: string; hint: string }[] = [
  { test: /web page, not a downloadable file/i, title: 'This link is a web page',
    hint: 'Video and streaming sites (YouTube and similar) show a page, not a file. Paste the direct file link instead. Capturing media from sites comes with the browser extension.' },
  { test: /HTTP 401/, title: 'Sign-in required', hint: 'The server needs you to be logged in to download this file.' },
  { test: /HTTP 403/, title: 'Access denied by the server', hint: 'The file is restricted, needs a signed-in session, or the link expired. Copy a fresh link from the site and try again.' },
  { test: /HTTP 404/, title: 'File not found', hint: 'The link is broken, or the file was moved or deleted.' },
  { test: /HTTP 410/, title: 'File no longer available', hint: 'The server says this file was permanently removed.' },
  { test: /HTTP 416/, title: 'Server rejected the byte range', hint: 'The server cannot resume this file. Retry to start again from the beginning.' },
  { test: /HTTP 429/, title: 'Too many requests', hint: 'The server is rate-limiting you. Wait a little and retry, or lower the connection count in Settings.' },
  { test: /HTTP 5\d\d/, title: 'Server error', hint: 'The website is having problems. Retry later.' },
  { test: /HTTP 400/, title: 'The server rejected the request', hint: 'The link may be incomplete or no longer valid.' },
  { test: /not allowed/i, title: 'Blocked address', hint: 'This link points to a private or local network address, which is blocked for safety.' },
  { test: /integrity|checksum/i, title: 'File failed verification', hint: 'The downloaded data did not match what the server advertised. Retry to download it again.' },
  { test: /no such host|lookup .* (failed|no such)/i, title: 'Server not found', hint: 'Check the address and your internet connection.' },
  { test: /timed? ?out|timeout/i, title: 'Connection timed out', hint: 'The server stopped responding. Retry; the download resumes where it stopped.' },
  { test: /reset|forcibly closed|broken pipe|unexpected EOF/i, title: 'Connection dropped', hint: 'The connection was interrupted. Retry; finished parts are kept.' },
  { test: /redirect rejected|too many redirects/i, title: 'Unsafe or endless redirect', hint: 'The link redirected somewhere unsafe, or in a loop.' },
  { test: /certificate|x509|tls/i, title: 'Secure connection failed', hint: 'The site’s security certificate is not valid.' },
  { test: /changed/i, title: 'File changed on the server', hint: 'The server now has a different version. Retry to download the new version from the start.' },
  { test: /no space|disk full/i, title: 'Disk full', hint: 'Free up space on the destination drive, then retry.' },
  { test: /access is denied|permission denied/i, title: 'Cannot write to the folder', hint: 'Choose a different download folder in Settings.' },
]

export function explainError(raw: string): Explanation {
  for (const rule of RULES) {
    if (rule.test.test(raw)) return { title: rule.title, hint: rule.hint }
  }
  return { title: 'Download failed', hint: raw }
}
