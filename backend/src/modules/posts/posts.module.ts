import { Module } from '@nestjs/common';
import { JwtModule } from '@nestjs/jwt';
import { PrismaModule } from '../../config/prisma.module';
import {
  AuthorPostsController,
  CommunityPostsController,
  PostsController,
  UniversityPostsController,
} from './posts.controller';
import { CommentsController } from './comments.controller';
import { PostsService } from './posts.service';
import { LikesService } from './likes.service';
import { CommentsService } from './comments.service';

@Module({
  imports: [PrismaModule, JwtModule.register({})],
  controllers: [
    PostsController,
    CommentsController,
    AuthorPostsController,
    UniversityPostsController,
    CommunityPostsController,
  ],
  providers: [PostsService, LikesService, CommentsService],
  exports: [PostsService],
})
export class PostsModule {}
