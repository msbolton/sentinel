import {
  Controller,
  Post,
  Get,
  Body,
  Param,
  UseGuards,
  HttpException,
  HttpStatus,
  Logger,
} from '@nestjs/common';
import { Throttle, ThrottlerGuard } from '@nestjs/throttler';
import { KeycloakAdminService } from './keycloak-admin.service';
import { JwtAuthGuard } from './jwt-auth.guard';
import { Roles } from './decorators/roles.decorator';
import { MailerService } from '@nestjs-modules/mailer';

interface RegisterDto {
  username: string;
  email: string;
  password: string;
  confirmPassword: string;
  firstName: string;
  lastName: string;
  organization: string;
  justification: string;
}

@Controller('auth')
export class RegistrationController {
  private readonly logger = new Logger(RegistrationController.name);

  constructor(
    private readonly keycloakAdmin: KeycloakAdminService,
    private readonly mailerService: MailerService,
  ) {}

  @Post('register')
  @UseGuards(ThrottlerGuard)
  @Throttle({ default: { limit: 5, ttl: 900000 } })
  async register(@Body() dto: RegisterDto): Promise<{ message: string }> {
    if (dto.password !== dto.confirmPassword) {
      throw new HttpException('Passwords do not match', HttpStatus.BAD_REQUEST);
    }

    if (dto.password.length < 8) {
      throw new HttpException(
        'Password must be at least 8 characters',
        HttpStatus.BAD_REQUEST,
      );
    }

    await this.keycloakAdmin.createUser({
      username: dto.username,
      email: dto.email,
      firstName: dto.firstName,
      lastName: dto.lastName,
      password: dto.password,
      organization: dto.organization,
      justification: dto.justification,
    });

    this.logger.log(`Registration submitted for user: ${dto.username}`);

    return { message: 'Registration submitted successfully. Pending admin approval.' };
  }

  @Get('pending-registrations')
  @UseGuards(JwtAuthGuard)
  @Roles('sentinel-admin')
  async getPendingRegistrations() {
    return this.keycloakAdmin.getPendingRegistrations();
  }

  @Post('approve-registration/:userId')
  @UseGuards(JwtAuthGuard)
  @Roles('sentinel-admin')
  async approveRegistration(
    @Param('userId') userId: string,
  ): Promise<{ message: string }> {
    await this.keycloakAdmin.approveUser(userId);

    this.logger.log(`User ${userId} approved`);

    return { message: `User ${userId} has been approved.` };
  }

  @Post('reject-registration/:userId')
  @UseGuards(JwtAuthGuard)
  @Roles('sentinel-admin')
  async rejectRegistration(
    @Param('userId') userId: string,
  ): Promise<{ message: string }> {
    const { email, firstName } = await this.keycloakAdmin.rejectUser(userId);

    try {
      await this.mailerService.sendMail({
        to: email,
        subject: 'Sentinel — Registration Request Rejected',
        text: `Dear ${firstName},\n\nYour registration request for Sentinel has been reviewed and unfortunately rejected.\n\nIf you believe this is an error, please contact your administrator.\n\nSentinel Security Team`,
      });
      this.logger.log(`User ${userId} rejected; notification sent to ${email}`);
    } catch (mailError) {
      this.logger.warn(`User ${userId} rejected but notification email to ${email} failed: ${mailError}`);
    }

    return { message: `User ${userId} has been rejected.` };
  }
}
