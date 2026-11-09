import {
  Body,
  Controller,
  Delete,
  Get,
  HttpCode,
  HttpStatus,
  Param,
  ParseUUIDPipe,
  Patch,
  Post,
  Query,
  UseGuards,
} from '@nestjs/common';
import { ApiBearerAuth, ApiOperation, ApiTags } from '@nestjs/swagger';
import { PostsService } from './posts.service';
import { LikesService } from './likes.service';
import { CommentsService } from './comments.service';
import {
  CreateCommentDto,
  CreatePostDto,
  UpdatePostDto,
} from './dto/post.dto';
import { JwtAccessGuard } from '../../common/guards/jwt-access.guard';
import { CurrentUser } from '../../common/decorators/current-user.decorator';
import type { AccessTokenPayload } from '../../common/guards/jwt-access.guard';
import { parsePage } from '../../common/pagination/cursor';

@ApiTags('posts')
@ApiBearerAuth('access-token')
@Controller('posts')
@UseGuards(JwtAccessGuard)
export class PostsController {
  constructor(
    private readonly posts: PostsService,
    private readonly likes: LikesService,
    private readonly comments: CommentsService,
  ) {}

  @Post()
  @ApiOperation({ summary: 'Create a post' })
  async create(
    @CurrentUser() user: AccessTokenPayload,
    @Body() dto: CreatePostDto,
  ) {
    return this.posts.create(user.sub, dto);
  }

  @Get(':id')
  @ApiOperation({ summary: 'Post detail (visibility-enforced)' })
  async detail(
    @CurrentUser() user: AccessTokenPayload,
    @Param('id', ParseUUIDPipe) id: string,
  ) {
    return this.posts.detail(user.sub, id);
  }

  @Patch(':id')
  @ApiOperation({ summary: 'Update own post' })
  async update(
    @CurrentUser() user: AccessTokenPayload,
    @Param('id', ParseUUIDPipe) id: string,
    @Body() dto: UpdatePostDto,
  ) {
    return this.posts.update(user.sub, id, dto);
  }

  @Delete(':id')
  @HttpCode(HttpStatus.OK)
  @ApiOperation({ summary: 'Soft-delete own post' })
  async remove(
    @CurrentUser() user: AccessTokenPayload,
    @Param('id', ParseUUIDPipe) id: string,
  ) {
    return this.posts.remove(user.sub, id);
  }

  @Post(':id/like')
  @HttpCode(HttpStatus.OK)
  @ApiOperation({ summary: 'Like a post (idempotent)' })
  async like(
    @CurrentUser() user: AccessTokenPayload,
    @Param('id', ParseUUIDPipe) id: string,
  ) {
    return this.likes.like(user.sub, id);
  }

  @Delete(':id/like')
  @HttpCode(HttpStatus.OK)
  @ApiOperation({ summary: 'Unlike a post (idempotent)' })
  async unlike(
    @CurrentUser() user: AccessTokenPayload,
    @Param('id', ParseUUIDPipe) id: string,
  ) {
    return this.likes.unlike(user.sub, id);
  }

  @Post(':id/comments')
  @ApiOperation({ summary: 'Comment on a post' })
  async comment(
    @CurrentUser() user: AccessTokenPayload,
    @Param('id', ParseUUIDPipe) id: string,
    @Body() dto: CreateCommentDto,
  ) {
    return this.comments.create(user.sub, id, dto);
  }

  @Get(':id/comments')
  @ApiOperation({ summary: 'List post comments (oldest first)' })
  async commentsList(
    @CurrentUser() user: AccessTokenPayload,
    @Param('id', ParseUUIDPipe) id: string,
    @Query() query: { limit?: unknown; cursor?: unknown },
  ) {
    return this.comments.list(user.sub, id, parsePage(query));
  }
}

@ApiTags('posts')
@ApiBearerAuth('access-token')
@Controller('users')
@UseGuards(JwtAccessGuard)
export class AuthorPostsController {
  constructor(private readonly posts: PostsService) {}

  @Get(':id/posts')
  @ApiOperation({ summary: "List an author's visible posts" })
  async byAuthor(
    @CurrentUser() user: AccessTokenPayload,
    @Param('id', ParseUUIDPipe) id: string,
    @Query() query: { limit?: unknown; cursor?: unknown },
  ) {
    return this.posts.listByAuthor(user.sub, id, parsePage(query));
  }
}

@ApiTags('posts')
@ApiBearerAuth('access-token')
@Controller('universities')
@UseGuards(JwtAccessGuard)
export class UniversityPostsController {
  constructor(private readonly posts: PostsService) {}

  @Get(':slug/posts')
  @ApiOperation({ summary: 'List university posts (members only)' })
  async byUniversity(
    @CurrentUser() user: AccessTokenPayload,
    @Param('slug') slug: string,
    @Query() query: { limit?: unknown; cursor?: unknown },
  ) {
    return this.posts.listByUniversity(user.sub, slug, parsePage(query));
  }
}

@ApiTags('posts')
@ApiBearerAuth('access-token')
@Controller('communities')
@UseGuards(JwtAccessGuard)
export class CommunityPostsController {
  constructor(private readonly posts: PostsService) {}

  @Get(':id/posts')
  @ApiOperation({ summary: 'List community posts' })
  async byCommunity(
    @CurrentUser() user: AccessTokenPayload,
    @Param('id', ParseUUIDPipe) id: string,
    @Query() query: { limit?: unknown; cursor?: unknown },
  ) {
    return this.posts.listByCommunity(user.sub, id, parsePage(query));
  }
}
