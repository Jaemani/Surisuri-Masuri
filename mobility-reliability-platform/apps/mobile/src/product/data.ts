import type {
  DeviceSummary,
  DeviceTimelineItem,
  RepairJob,
  RepairWorkOrder,
  RepairerRoleSession,
  UserRoleSession,
  SubsidySummary,
} from './types';

// Demo records are intentionally deterministic so the product prototype is easy to review.
export const demoRepairRequest: RepairWorkOrder = {
  id: 'demo-request-042',
  title: '오른쪽 바퀴에서 소리가 나요',
  createdAt: '2026년 8월 12일',
  status: 'assigned',
  repairer: '따뜻한바퀴 수리센터',
  visitAt: '8월 16일(일) 오후 2:00',
};

export const demoTimeline: DeviceTimelineItem[] = [
  {
    id: 'timeline-1',
    date: '2026. 08. 12',
    title: '수리 요청을 접수했어요',
    detail: '오른쪽 바퀴 소음 · 사진 2장',
    tone: 'orange',
  },
  {
    id: 'timeline-2',
    date: '2026. 07. 28',
    title: '정기 점검을 마쳤어요',
    detail: '브레이크와 타이어 상태 양호',
    tone: 'teal',
  },
  {
    id: 'timeline-3',
    date: '2026. 04. 05',
    title: '내 기기를 등록했어요',
    detail: '나래 모빌리티 M-22 · 등록번호 MR-2208',
    tone: 'blue',
  },
];

export const demoSubsidy: SubsidySummary = {
  program: '전동보장구 수리 지원금',
  cycle: '2026년 상반기',
  used: 180000,
  total: 300000,
  nextReview: '2026년 9월 1일',
  note: '남은 지원금은 수리센터에서 확인할 수 있어요.',
};

export const demoDevice: DeviceSummary = {
  id: 'demo-device-2208',
  name: '나래 모빌리티 M-22',
  registrationNumber: 'MR-2208',
  registeredAt: '2024년 등록',
  status: 'healthy',
  timeline: demoTimeline,
};

export const demoUserRoleSession: UserRoleSession = {
  role: 'user',
  displayName: '김정자 님',
  isDemo: true,
};

export const demoRepairerRoleSession: RepairerRoleSession = {
  role: 'repairer',
  displayName: '따뜻한바퀴 수리센터',
  isDemo: true,
};

export const demoRepairJobs: RepairJob[] = [
  {
    id: 'job-101',
    customer: '김정자 님',
    device: '나래 모빌리티 M-22',
    issue: '오른쪽 바퀴 소음 점검',
    due: '오늘 오후 2:00',
    priority: 'today',
  },
  {
    id: 'job-102',
    customer: '박영수 님',
    device: '케어라이드 S-4',
    issue: '배터리 교체 상담',
    due: '내일 오전 10:30',
    priority: 'scheduled',
  },
  {
    id: 'job-103',
    customer: '이순옥 님',
    device: '나래 모빌리티 M-18',
    issue: '정기 점검',
    due: '8월 19일 오후 1:00',
    priority: 'scheduled',
  },
];

export const money = (value: number) => `${value.toLocaleString('ko-KR')}원`;
