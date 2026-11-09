import {
  ViewerContext,
  canViewPost,
  isBlockedEitherWay,
} from './viewer-context';

function ctx(over: Partial<ViewerContext> = {}): ViewerContext {
  return {
    userId: 'viewer',
    universityIds: [],
    communityIds: [],
    followingIds: [],
    blockedIds: [],
    ...over,
  };
}

describe('canViewPost', () => {
  const base = {
    authorId: 'author',
    universityId: 'uni-a',
    communityId: null as string | null,
    visibility: 'PUBLIC' as const,
    deletedAt: null as Date | null,
  };

  it('hides deleted posts from everyone including the author', () => {
    const deleted = { ...base, deletedAt: new Date() };
    expect(canViewPost(ctx(), deleted)).toBe(false);
    expect(
      canViewPost(ctx({ userId: 'author' }), { ...deleted, authorId: 'author' }),
    ).toBe(false);
  });

  it('authors always see their own posts', () => {
    const c = ctx({ userId: 'author' });
    for (const v of ['PUBLIC', 'UNIVERSITY', 'COMMUNITY', 'FOLLOWERS'] as const) {
      expect(canViewPost(c, { ...base, authorId: 'author', visibility: v })).toBe(true);
    }
  });

  it('blocks hide content in both directions', () => {
    const blocked = ctx({ blockedIds: ['author'] });
    expect(canViewPost(blocked, base)).toBe(false);
    expect(isBlockedEitherWay(blocked, 'author')).toBe(true);
    expect(isBlockedEitherWay(blocked, 'stranger')).toBe(false);
    expect(isBlockedEitherWay(blocked, 'viewer')).toBe(false);
  });

  it('enforces university/community/follower scoping', () => {
    expect(
      canViewPost(ctx(), { ...base, visibility: 'UNIVERSITY' }),
    ).toBe(false);
    expect(
      canViewPost(ctx({ universityIds: ['uni-a'] }), {
        ...base,
        visibility: 'UNIVERSITY',
      }),
    ).toBe(true);
    expect(
      canViewPost(ctx({ universityIds: ['uni-b'] }), {
        ...base,
        visibility: 'UNIVERSITY',
      }),
    ).toBe(false);
    expect(
      canViewPost(ctx({ communityIds: ['c1'] }), {
        ...base,
        visibility: 'COMMUNITY',
        communityId: 'c1',
      }),
    ).toBe(true);
    expect(
      canViewPost(ctx({ communityIds: ['c2'] }), {
        ...base,
        visibility: 'COMMUNITY',
        communityId: 'c1',
      }),
    ).toBe(false);
    expect(
      canViewPost(ctx(), { ...base, visibility: 'COMMUNITY', communityId: null }),
    ).toBe(false);
    expect(
      canViewPost(ctx({ followingIds: ['author'] }), {
        ...base,
        visibility: 'FOLLOWERS',
      }),
    ).toBe(true);
    expect(
      canViewPost(ctx(), { ...base, visibility: 'FOLLOWERS' }),
    ).toBe(false);
  });
});
