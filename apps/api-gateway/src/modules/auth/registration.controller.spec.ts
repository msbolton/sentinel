import { Test, TestingModule } from '@nestjs/testing';
import { Reflector } from '@nestjs/core';
import { ThrottlerModule, ThrottlerGuard } from '@nestjs/throttler';
import { RegistrationController } from './registration.controller';
import { KeycloakAdminService } from './keycloak-admin.service';
import { JwtAuthGuard } from './jwt-auth.guard';
import { AuthAuditService } from './auth-audit.service';
import { MailerService } from '@nestjs-modules/mailer';

describe('RegistrationController', () => {
  let controller: RegistrationController;
  let keycloakAdmin: {
    createUser: jest.Mock;
    getPendingRegistrations: jest.Mock;
    approveUser: jest.Mock;
    rejectUser: jest.Mock;
  };
  let mailerService: { sendMail: jest.Mock };

  beforeEach(async () => {
    keycloakAdmin = {
      createUser: jest.fn().mockResolvedValue(undefined),
      getPendingRegistrations: jest.fn().mockResolvedValue([]),
      approveUser: jest.fn().mockResolvedValue(undefined),
      rejectUser: jest.fn().mockResolvedValue({ email: 'user@test.com', firstName: 'Test' }),
    };
    mailerService = { sendMail: jest.fn().mockResolvedValue(undefined) };

    const module: TestingModule = await Test.createTestingModule({
      imports: [ThrottlerModule.forRoot([{ ttl: 1, limit: 100 }])],
      controllers: [RegistrationController],
      providers: [
        { provide: KeycloakAdminService, useValue: keycloakAdmin },
        { provide: MailerService, useValue: mailerService },
        {
          provide: JwtAuthGuard,
          useValue: { canActivate: jest.fn().mockReturnValue(true) },
        },
        {
          provide: AuthAuditService,
          useValue: {
            logAccess: jest.fn(),
            logAccessDenied: jest.fn(),
            logRoleCheckFailed: jest.fn(),
            logClassificationCheckFailed: jest.fn(),
          },
        },
        Reflector,
      ],
    }).compile();

    controller = module.get<RegistrationController>(RegistrationController);
  });

  describe('POST /register', () => {
    const validDto = {
      username: 'jdoe',
      email: 'jdoe@example.com',
      password: 'SecurePass1!',
      confirmPassword: 'SecurePass1!',
      firstName: 'John',
      lastName: 'Doe',
      organization: 'ACME Corp',
      justification: 'Need access for project Alpha',
    };

    it('should return a message containing "submitted" on success', async () => {
      const result = await controller.register(validDto);
      expect(result.message).toContain('submitted');
      expect(keycloakAdmin.createUser).toHaveBeenCalledWith({
        username: validDto.username,
        email: validDto.email,
        firstName: validDto.firstName,
        lastName: validDto.lastName,
        password: validDto.password,
        organization: validDto.organization,
        justification: validDto.justification,
      });
    });

    it('should throw when passwords do not match', async () => {
      const dto = { ...validDto, confirmPassword: 'DifferentPass1!' };

      await expect(controller.register(dto)).rejects.toThrow();
      expect(keycloakAdmin.createUser).not.toHaveBeenCalled();
    });

    it('should throw when password is less than 8 characters', async () => {
      const dto = { ...validDto, password: 'short', confirmPassword: 'short' };

      await expect(controller.register(dto)).rejects.toThrow();
      expect(keycloakAdmin.createUser).not.toHaveBeenCalled();
    });
  });

  describe('GET /pending-registrations', () => {
    it('should return the list from keycloakAdmin', async () => {
      const pendingUsers = [
        { id: 'user-1', username: 'jdoe', email: 'jdoe@example.com' },
        { id: 'user-2', username: 'jsmith', email: 'jsmith@example.com' },
      ];
      keycloakAdmin.getPendingRegistrations.mockResolvedValue(pendingUsers);

      const result = await controller.getPendingRegistrations();

      expect(keycloakAdmin.getPendingRegistrations).toHaveBeenCalled();
      expect(result).toEqual(pendingUsers);
    });

    it('should return empty array when no pending users', async () => {
      const result = await controller.getPendingRegistrations();

      expect(result).toEqual([]);
    });
  });

  describe('POST /approve-registration/:userId', () => {
    it('should call approveUser and return a message containing "approved"', async () => {
      const userId = 'user-abc-123';

      const result = await controller.approveRegistration(userId);

      expect(keycloakAdmin.approveUser).toHaveBeenCalledWith(userId);
      expect(result.message).toContain('approved');
    });
  });

  describe('POST /reject-registration/:userId', () => {
    it('should call rejectUser, send rejection email, and return a message containing "rejected"', async () => {
      const userId = 'user-xyz-456';
      const rejectedUser = { email: 'user@test.com', firstName: 'Test' };
      keycloakAdmin.rejectUser.mockResolvedValue(rejectedUser);

      const result = await controller.rejectRegistration(userId);

      expect(keycloakAdmin.rejectUser).toHaveBeenCalledWith(userId);
      expect(mailerService.sendMail).toHaveBeenCalledWith(
        expect.objectContaining({ to: rejectedUser.email }),
      );
      expect(result.message).toContain('rejected');
    });

    it('should send email to the address returned by rejectUser', async () => {
      const userId = 'user-xyz-789';
      const rejectedUser = { email: 'specific@example.com', firstName: 'Specific' };
      keycloakAdmin.rejectUser.mockResolvedValue(rejectedUser);

      await controller.rejectRegistration(userId);

      expect(mailerService.sendMail).toHaveBeenCalledWith(
        expect.objectContaining({ to: 'specific@example.com' }),
      );
    });

    it('should still succeed when rejection email fails to send', async () => {
      const userId = 'user-mail-fail';
      keycloakAdmin.rejectUser.mockResolvedValue({ email: 'fail@test.com', firstName: 'Fail' });
      mailerService.sendMail.mockRejectedValue(new Error('SMTP connection refused'));

      const result = await controller.rejectRegistration(userId);

      expect(keycloakAdmin.rejectUser).toHaveBeenCalledWith(userId);
      expect(result.message).toContain('rejected');
    });
  });
});
