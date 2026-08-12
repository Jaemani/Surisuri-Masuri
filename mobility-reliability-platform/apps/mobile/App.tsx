import { StatusBar } from 'expo-status-bar';
import { useState } from 'react';
import { ActivityIndicator, Alert, Platform, SafeAreaView, StyleSheet, Text, View } from 'react-native';

import { useTripRecorder } from './src/telemetry/useTripRecorder';
import { RepairerScreen } from './src/product/components/RepairerScreen';
import {
  openPhoneSettings,
  UserDeviceScreen,
  UserHomeScreen,
  UserRepairScreen,
  UserSettingsScreen,
  UserSupportScreen,
} from './src/product/components/UserScreens';
import { BottomNavigation, colors } from './src/product/components/ProductUi';
import { createProductRepository } from './src/product/repository';
import { useProductData } from './src/product/useProductData';
import type { CreateRepairRequestInput, UserTab } from './src/product/types';

const productRepository = createProductRepository();

export default function App() {
  const recorder = useTripRecorder();
  const product = useProductData(productRepository);
  const [activeTab, setActiveTab] = useState<UserTab>('home');

  const switchToRepairer = () => void product.setRole('repairer');
  const switchToUser = () => {
    void product.setRole('user');
    setActiveTab('home');
  };

  const stopWithConfirmation = () => {
    if (Platform.OS === 'web') {
      void recorder.stop();
      return;
    }
    Alert.alert('이동 기록을 마칠까요?', '마친 뒤에도 지금까지의 기록은 내 기기에서 확인할 수 있어요.', [
      { text: '계속 기록', style: 'cancel' },
      { text: '기록 마치기', style: 'destructive', onPress: () => void recorder.stop() },
    ]);
  };

  if (product.phase === 'error') {
    return <ProductError onRetry={() => void product.refresh()} />;
  }

  if (product.phase === 'loading' || !product.view) {
    return <ProductLoading />;
  }

  const view = product.view;

  if (view.role === 'repairer') {
    return (
      <SafeAreaView style={styles.screen}>
        <StatusBar style="dark" />
        <RepairerScreen jobs={view.repairJobs} displayName={view.displayName} isDemo={view.isDemo} onSwitchToUser={switchToUser} onTransition={product.transitionRepairJob} onRefresh={product.refresh} />
      </SafeAreaView>
    );
  }

  const openRepairs = () => setActiveTab('repairs');
  const createRequest = (input: CreateRepairRequestInput) => product.createRepairRequest(input);

  return (
    <SafeAreaView style={styles.screen}>
      <StatusBar style="dark" />
      <View style={styles.body}>
        {activeTab === 'home' ? (
          <UserHomeScreen
            state={recorder.state}
            lastCompletedSession={recorder.state.lastCompletedSession}
            displayName={view.displayName}
            request={view.repairRequest}
            device={view.device}
            subsidy={view.subsidy}
            onStart={() => void recorder.start()}
            onResume={() => void recorder.resume()}
            onStop={stopWithConfirmation}
            onOpenRepairs={openRepairs}
          />
        ) : activeTab === 'repairs' ? (
          <UserRepairScreen isDemo={view.isDemo} request={view.repairRequest} onCreateRequest={createRequest} onRefresh={product.refresh} />
        ) : activeTab === 'device' ? (
          <UserDeviceScreen device={view.device} onOpenRepairs={openRepairs} />
        ) : activeTab === 'support' ? (
          <UserSupportScreen subsidy={view.subsidy} />
        ) : (
          <UserSettingsScreen
            state={recorder.state}
            onEnableBackground={() => void recorder.enableBackground()}
            onOpenPhoneSettings={() => void openPhoneSettings()}
            onSwitchToRepairer={switchToRepairer}
          />
        )}
      </View>
      <BottomNavigation activeTab={activeTab} onChange={setActiveTab} />
    </SafeAreaView>
  );
}

function ProductLoading() {
  return <SafeAreaView style={styles.fallback}><ActivityIndicator color={colors.teal} /><Text style={styles.fallbackText}>내 정보를 준비하고 있어요…</Text></SafeAreaView>;
}

function ProductError({ onRetry }: { onRetry: () => void }) {
  return <SafeAreaView style={styles.fallback}><Text accessibilityRole="header" style={styles.fallbackTitle}>잠시 문제가 생겼어요</Text><Text style={styles.fallbackText}>정보를 불러오지 못했어요. 다시 시도해 주세요.</Text><Text accessibilityRole="button" onPress={onRetry} style={styles.retry}>다시 시도</Text></SafeAreaView>;
}

const styles = StyleSheet.create({
  screen: { backgroundColor: colors.canvas, flex: 1 },
  body: { flex: 1 },
  fallback: { alignItems: 'center', backgroundColor: colors.canvas, flex: 1, justifyContent: 'center', padding: 24 },
  fallbackTitle: { color: colors.ink, fontSize: 23, fontWeight: '800' },
  fallbackText: { color: colors.muted, fontSize: 15, marginTop: 10, textAlign: 'center' },
  retry: { color: colors.teal, fontSize: 16, fontWeight: '800', marginTop: 22, padding: 12 },
});
