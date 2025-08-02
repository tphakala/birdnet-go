import { describe, it, expect, vi, beforeEach } from 'vitest';
import { getLogger, loggers } from './logger';

describe('Logger', () => {
  let consoleLogSpy: ReturnType<typeof vi.spyOn>;
  let consoleInfoSpy: ReturnType<typeof vi.spyOn>;
  let consoleWarnSpy: ReturnType<typeof vi.spyOn>;
  let consoleErrorSpy: ReturnType<typeof vi.spyOn>;
  let consoleGroupSpy: ReturnType<typeof vi.spyOn>;
  let consoleGroupEndSpy: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    consoleLogSpy = vi.spyOn(console, 'log').mockImplementation(() => {});
    consoleInfoSpy = vi.spyOn(console, 'info').mockImplementation(() => {});
    consoleWarnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {});
    consoleErrorSpy = vi.spyOn(console, 'error').mockImplementation(() => {});
    consoleGroupSpy = vi.spyOn(console, 'group').mockImplementation(() => {});
    consoleGroupEndSpy = vi.spyOn(console, 'groupEnd').mockImplementation(() => {});
    vi.clearAllMocks();
  });

  describe('getLogger', () => {
    it('should create a logger with the specified category', () => {
      const logger = getLogger('test');
      expect(logger).toBeDefined();
      expect(logger.debug).toBeDefined();
      expect(logger.info).toBeDefined();
      expect(logger.warn).toBeDefined();
      expect(logger.error).toBeDefined();
    });

    it('should prefix log messages with category', () => {
      const logger = getLogger('test-category');

      // In dev mode, these should log
      if (import.meta.env.DEV) {
        logger.debug('debug message');
        expect(consoleLogSpy).toHaveBeenCalledWith('[test-category]', 'debug message');

        logger.info('info message');
        expect(consoleInfoSpy).toHaveBeenCalledWith('[test-category]', 'info message');
      }

      // Warnings and errors always log
      logger.warn('warning message');
      expect(consoleWarnSpy).toHaveBeenCalledWith('[test-category]', 'warning message', undefined);
    });
  });

  describe('error logging', () => {
    it('should log errors with context', () => {
      const logger = getLogger('test');
      const error = new Error('Test error');
      const context = { userId: '123', action: 'save' };

      logger.error('Failed to save', error, context);

      expect(consoleErrorSpy).toHaveBeenCalledWith(
        '[test]',
        'Failed to save',
        error,
        expect.objectContaining({
          message: 'Failed to save',
          category: 'test',
          userId: '123',
          action: 'save',
          timestamp: expect.any(String),
        })
      );
    });

    it('should handle non-Error objects', () => {
      const logger = getLogger('test');
      const errorObj = { code: 'NETWORK_ERROR', details: 'Connection failed' };

      logger.error('Network error occurred', errorObj);

      expect(consoleErrorSpy).toHaveBeenCalledWith('[test]', 'Network error occurred', errorObj);
    });

    it('should handle error message only', () => {
      const logger = getLogger('test');

      logger.error('Simple error message');

      expect(consoleErrorSpy).toHaveBeenCalledWith(
        '[test]',
        'Simple error message',
        expect.objectContaining({
          message: 'Simple error message',
          category: 'test',
          timestamp: expect.any(String),
        })
      );
    });
  });

  describe('development-only methods', () => {
    it('should handle group/groupEnd in dev mode', () => {
      const logger = getLogger('test');

      if (import.meta.env.DEV) {
        logger.group('Test Group');
        expect(consoleGroupSpy).toHaveBeenCalledWith('[test] Test Group');

        logger.groupEnd();
        expect(consoleGroupEndSpy).toHaveBeenCalled();
      }
    });

    it('should handle timing in dev mode', () => {
      const logger = getLogger('test');

      if (import.meta.env.DEV) {
        logger.time('operation');
        // Simulate some delay
        logger.timeEnd('operation');

        expect(consoleLogSpy).toHaveBeenCalled();
        const callArgs = consoleLogSpy.mock.calls[0];
        expect(callArgs[0]).toContain('[test] operation:');
        expect(callArgs[0]).toContain('ms');
      }
    });
  });

  describe('predefined loggers', () => {
    it('should provide common logger instances', () => {
      expect(loggers.api).toBeDefined();
      expect(loggers.auth).toBeDefined();
      expect(loggers.sse).toBeDefined();
      expect(loggers.audio).toBeDefined();
      expect(loggers.ui).toBeDefined();
      expect(loggers.settings).toBeDefined();
      expect(loggers.performance).toBeDefined();
    });
  });

  describe('multiple arguments', () => {
    it('should support multiple arguments in log methods', () => {
      const logger = getLogger('test');
      const obj = { foo: 'bar' };
      const arr = [1, 2, 3];

      if (import.meta.env.DEV) {
        logger.debug('Multiple', 'arguments', obj, arr);
        expect(consoleLogSpy).toHaveBeenCalledWith('[test]', 'Multiple', 'arguments', obj, arr);
      }

      logger.warn('Warning with', 'multiple args', 123);
      expect(consoleWarnSpy).toHaveBeenCalledWith('[test]', 'Warning with', 'multiple args', 123);
    });
  });
});
