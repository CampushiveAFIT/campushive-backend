import { Module } from '@nestjs/common';
import { JwtModule } from '@nestjs/jwt';
import { PrismaModule } from '../../config/prisma.module';
import { UniversitiesModule } from '../universities/universities.module';
import { UsersController } from './users.controller';
import { UsersService } from './users.service';
import { UserGraphController } from './user-graph.controller';
import { UniversityIdentityController } from './university-identity.controller';
import { UserGraphService } from './user-graph.service';

@Module({
  imports: [PrismaModule, JwtModule.register({}), UniversitiesModule],
  controllers: [UsersController, UserGraphController, UniversityIdentityController],
  providers: [UsersService, UserGraphService],
  exports: [UsersService, UserGraphService],
})
export class UsersModule {}