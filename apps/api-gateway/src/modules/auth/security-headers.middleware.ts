import { Injectable, NestMiddleware } from '@nestjs/common';
import { Request, Response, NextFunction } from 'express';

/**
 * Middleware that applies security headers to all responses.
 * These headers mitigate common web attacks (XSS, clickjacking, MIME sniffing).
 */
@Injectable()
export class SecurityHeadersMiddleware implements NestMiddleware {
  use(_req: Request, res: Response, next: NextFunction): void {
    // Prevent MIME sniffing
    res.setHeader('X-Content-Type-Options', 'nosniff');

    // Prevent clickjacking
    res.setHeader('X-Frame-Options', 'DENY');

    // Enable XSS filter (legacy browsers)
    res.setHeader('X-XSS-Protection', '1; mode=block');

    // Referrer policy — only send origin for cross-origin
    res.setHeader('Referrer-Policy', 'strict-origin-when-cross-origin');

    // Prevent the browser from caching sensitive responses
    res.setHeader('Cache-Control', 'no-store, no-cache, must-revalidate, proxy-revalidate');
    res.setHeader('Pragma', 'no-cache');

    // HSTS — enforce HTTPS (1 year, include subdomains)
    // Only applied in production to avoid dev SSL issues
    if (process.env['NODE_ENV'] === 'production') {
      res.setHeader(
        'Strict-Transport-Security',
        'max-age=31536000; includeSubDomains',
      );
    }

    // Permissions policy — restrict browser features
    res.setHeader(
      'Permissions-Policy',
      'camera=(), microphone=(), geolocation=(self), payment=()',
    );

    next();
  }
}
