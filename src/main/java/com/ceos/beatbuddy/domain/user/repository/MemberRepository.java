package com.ceos.beatbuddy.domain.user.repository;

import com.ceos.beatbuddy.domain.user.entity.Member;
import org.springframework.data.jpa.repository.JpaRepository;

public interface MemberRepository extends JpaRepository<Member, Long> {
    Long findByLoginId(String loginId);
}
