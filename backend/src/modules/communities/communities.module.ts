import { Module } from '@nestjs/common';
import { JwtModule } from '@nestjs/jwt';
import { PrismaModule } from '../../config/prisma.module';
import { UniversitiesModule } from '../universities/universities.module';
import {
  CommunitiesController,
  UniversityCommunitiesController,
} from './communities.controller';
import { CommunitiesService } from './communities.service';

@Module({
  imports: [PrismaModule, JwtModule.register({}), UniversitiesModule],
  controllers: [CommunitiesController, UniversityCommunitiesController],
  providers: [CommunitiesService],
  exports: [CommunitiesService],
})
export class CommunitiesModule {}
