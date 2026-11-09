import { Module } from '@nestjs/common';
import { JwtModule } from '@nestjs/jwt';
import { PrismaModule } from '../../config/prisma.module';
import { PostsModule } from '../posts/posts.module';
import { EventsModule } from '../events/events.module';
import { FeedController } from './feed.controller';
import { SearchController } from './search.controller';
import { HomeFeedService } from './services/home-feed.service';
import { UniversityFeedService } from './services/university-feed.service';
import { CommunityFeedService } from './services/community-feed.service';
import { ExploreFeedService } from './services/explore-feed.service';
import { FeedCandidateService } from './services/feed-candidate.service';
import { FeedVisibilityService } from './services/feed-visibility.service';
import { FeedRankingService } from './services/feed-ranking.service';
import { FeedResponseService } from './services/feed-response.service';
import { SearchService } from './services/search.service';

@Module({
  imports: [PrismaModule, JwtModule.register({}), PostsModule, EventsModule],
  controllers: [FeedController, SearchController],
  providers: [
    HomeFeedService,
    UniversityFeedService,
    CommunityFeedService,
    ExploreFeedService,
    FeedCandidateService,
    FeedVisibilityService,
    FeedRankingService,
    FeedResponseService,
    SearchService,
  ],
  exports: [],
})
export class FeedModule {}
