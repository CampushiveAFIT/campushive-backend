import { directKey } from './direct-key';

describe('directKey', () => {
  it('orders ids deterministically regardless of argument order', () => {
    expect(directKey('b', 'a')).toBe('a:b');
    expect(directKey('a', 'b')).toBe('a:b');
  });

  it('rejects self-pairs and empty ids', () => {
    expect(() => directKey('a', 'a')).toThrow();
    expect(() => directKey('', 'a')).toThrow();
  });
});
