import { StatusBar } from 'expo-status-bar';
import { useState } from 'react';
import { Alert, SafeAreaView, StyleSheet, View } from 'react-native';

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
import type { ProductRole, UserTab } from './src/product/types';

export default function App() {
  const recorder = useTripRecorder();
  const [role, setRole] = useState<ProductRole>('user');
  const [activeTab, setActiveTab] = useState<UserTab>('home');
  const [requestSubmitted, setRequestSubmitted] = useState(true);

  const switchToRepairer = () => setRole('repairer');
  const switchToUser = () => {
    setRole('user');
    setActiveTab('home');
  };

  const stopWithConfirmation = () => {
    Alert.alert('이동 기록을 마칠까요?', '마친 뒤에도 지금까지의 기록은 내 기기에서 확인할 수 있어요.', [
      { text: '계속 기록', style: 'cancel' },
      { text: '기록 마치기', style: 'destructive', onPress: () => void recorder.stop() },
    ]);
  };

  if (role === 'repairer') {
    return (
      <SafeAreaView style={styles.screen}>
        <StatusBar style="dark" />
        <RepairerScreen onSwitchToUser={switchToUser} />
      </SafeAreaView>
    );
  }

  const openRepairs = () => setActiveTab('repairs');
  const createRequest = () => setRequestSubmitted(true);

  return (
    <SafeAreaView style={styles.screen}>
      <StatusBar style="dark" />
      <View style={styles.body}>
        {activeTab === 'home' ? (
          <UserHomeScreen
            state={recorder.state}
            onStart={() => void recorder.start()}
            onResume={() => void recorder.resume()}
            onStop={stopWithConfirmation}
            onOpenRepairs={openRepairs}
          />
        ) : activeTab === 'repairs' ? (
          <UserRepairScreen requestSubmitted={requestSubmitted} onCreateRequest={createRequest} />
        ) : activeTab === 'device' ? (
          <UserDeviceScreen onOpenRepairs={openRepairs} />
        ) : activeTab === 'support' ? (
          <UserSupportScreen />
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

const styles = StyleSheet.create({
  screen: { backgroundColor: colors.canvas, flex: 1 },
  body: { flex: 1 },
});
