import { SecurityHeadersMiddleware } from './security-headers.middleware';

describe('SecurityHeadersMiddleware', () => {
  let middleware: SecurityHeadersMiddleware;
  let mockResponse: { setHeader: jest.Mock; headers: Record<string, string> };
  let mockRequest: Record<string, unknown>;
  let mockNext: jest.Mock;

  const originalNodeEnv = process.env['NODE_ENV'];

  beforeEach(() => {
    middleware = new SecurityHeadersMiddleware();
    mockResponse = {
      headers: {},
      setHeader: jest.fn((key: string, value: string) => {
        mockResponse.headers[key] = value;
      }),
    };
    mockRequest = {};
    mockNext = jest.fn();
  });

  afterEach(() => {
    process.env['NODE_ENV'] = originalNodeEnv;
  });

  it('should call next()', () => {
    middleware.use(mockRequest as any, mockResponse as any, mockNext);
    expect(mockNext).toHaveBeenCalledTimes(1);
  });

  it('should set X-Content-Type-Options to nosniff', () => {
    middleware.use(mockRequest as any, mockResponse as any, mockNext);
    expect(mockResponse.setHeader).toHaveBeenCalledWith(
      'X-Content-Type-Options',
      'nosniff',
    );
  });

  it('should set X-Frame-Options to DENY', () => {
    middleware.use(mockRequest as any, mockResponse as any, mockNext);
    expect(mockResponse.setHeader).toHaveBeenCalledWith(
      'X-Frame-Options',
      'DENY',
    );
  });

  it('should set X-XSS-Protection', () => {
    middleware.use(mockRequest as any, mockResponse as any, mockNext);
    expect(mockResponse.setHeader).toHaveBeenCalledWith(
      'X-XSS-Protection',
      '1; mode=block',
    );
  });

  it('should set Referrer-Policy to strict-origin-when-cross-origin', () => {
    middleware.use(mockRequest as any, mockResponse as any, mockNext);
    expect(mockResponse.setHeader).toHaveBeenCalledWith(
      'Referrer-Policy',
      'strict-origin-when-cross-origin',
    );
  });

  it('should set Cache-Control to prevent caching', () => {
    middleware.use(mockRequest as any, mockResponse as any, mockNext);
    expect(mockResponse.setHeader).toHaveBeenCalledWith(
      'Cache-Control',
      'no-store, no-cache, must-revalidate, proxy-revalidate',
    );
  });

  it('should set Pragma to no-cache', () => {
    middleware.use(mockRequest as any, mockResponse as any, mockNext);
    expect(mockResponse.setHeader).toHaveBeenCalledWith('Pragma', 'no-cache');
  });

  it('should set Permissions-Policy to restrict browser features', () => {
    middleware.use(mockRequest as any, mockResponse as any, mockNext);
    expect(mockResponse.setHeader).toHaveBeenCalledWith(
      'Permissions-Policy',
      'camera=(), microphone=(), geolocation=(self), payment=()',
    );
  });

  describe('Strict-Transport-Security', () => {
    it('should set HSTS header in production', () => {
      process.env['NODE_ENV'] = 'production';
      // Re-create middleware so env is re-read at call time
      middleware = new SecurityHeadersMiddleware();

      middleware.use(mockRequest as any, mockResponse as any, mockNext);
      expect(mockResponse.setHeader).toHaveBeenCalledWith(
        'Strict-Transport-Security',
        'max-age=31536000; includeSubDomains',
      );
    });

    it('should NOT set HSTS header in non-production', () => {
      process.env['NODE_ENV'] = 'development';
      middleware = new SecurityHeadersMiddleware();

      middleware.use(mockRequest as any, mockResponse as any, mockNext);
      const hstsCall = mockResponse.setHeader.mock.calls.find(
        ([key]: [string]) => key === 'Strict-Transport-Security',
      );
      expect(hstsCall).toBeUndefined();
    });

    it('should NOT set HSTS header when NODE_ENV is test', () => {
      process.env['NODE_ENV'] = 'test';
      middleware = new SecurityHeadersMiddleware();

      middleware.use(mockRequest as any, mockResponse as any, mockNext);
      const hstsCall = mockResponse.setHeader.mock.calls.find(
        ([key]: [string]) => key === 'Strict-Transport-Security',
      );
      expect(hstsCall).toBeUndefined();
    });
  });

  it('should set all required headers in a single call', () => {
    middleware.use(mockRequest as any, mockResponse as any, mockNext);

    // In non-production mode, expect 7 headers (no HSTS)
    const expectedHeaders = [
      'X-Content-Type-Options',
      'X-Frame-Options',
      'X-XSS-Protection',
      'Referrer-Policy',
      'Cache-Control',
      'Pragma',
      'Permissions-Policy',
    ];

    const setHeaderNames = mockResponse.setHeader.mock.calls.map(
      ([key]: [string]) => key,
    );
    for (const header of expectedHeaders) {
      expect(setHeaderNames).toContain(header);
    }
  });
});
